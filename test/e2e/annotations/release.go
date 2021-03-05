/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package annotations

import (
	"fmt"
	"strings"
	"time"

	"net/http"

	"github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"k8s.io/api/networking/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"k8s.io/ingress-nginx/test/e2e/framework"
)

const (
	oldNginxName  = "old-nginx"
	newNginxName  = "new-nginx"
	newTomcatName = "new-tomcat"

	oldNginxImage  = "registry.cn-hangzhou.aliyuncs.com/acs-sample/old-nginx"
	newNginxImage  = "registry.cn-hangzhou.aliyuncs.com/acs-sample/new-nginx"
	newTomcatImage = "registry.cn-hangzhou.aliyuncs.com/acs-sample/tomcat"

	nginxPort  = 80
	tomcatPort = 8080

	waitForLuaSync = 5 * time.Second
)

var _ = framework.DescribeAnnotation("Release", func() {
	f := framework.NewDefaultFramework("release")

	ginkgo.BeforeEach(func() {
		f.NewDeployment(oldNginxName, oldNginxImage, nginxPort, 1)
		f.NewDeployment(newNginxName, newNginxImage, nginxPort, 1)
		f.NewDeployment(newTomcatName, newTomcatImage, tomcatPort, 1)
	})

	ginkgo.It("shift 50% traffic to the new application for blue-green release", func() {
		host := "bg50.release.com"

		f.EnsureIngress(&v1beta1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      host,
				Namespace: f.Namespace,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/service-weight": fmt.Sprintf("%s: %d, %s: %d", oldNginxName, 50, newNginxName, 50),
				},
			},
			Spec: v1beta1.IngressSpec{
				Rules: []v1beta1.IngressRule{
					{
						Host: host,
						IngressRuleValue: v1beta1.IngressRuleValue{
							HTTP: &v1beta1.HTTPIngressRuleValue{
								Paths: []v1beta1.HTTPIngressPath{
									{
										Path: "/",
										Backend: v1beta1.IngressBackend{
											ServiceName: oldNginxName,
											ServicePort: intstr.FromInt(nginxPort),
										},
									},
									{
										Path: "/",
										Backend: v1beta1.IngressBackend{
											ServiceName: newNginxName,
											ServicePort: intstr.FromInt(nginxPort),
										},
									},
								},
							},
						},
					},
				},
			},
		})

		time.Sleep(waitForLuaSync)
		result := map[string]int{"old": 0, "new": 0}
		for index := 1; index <= 10; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				Expect()
			resp.Status(http.StatusOK)
			resl := strings.TrimSpace(resp.Body().Raw())
			result[resl] = result[resl] + 1
			time.Sleep(time.Second * 1)
		}
		if result["new"] <= 0 {
			Expect(func() (bool, error) { return result["new"] > 0, nil }).Should(BeTrue())
		}
		if result["old"] <= 0 {
			Expect(func() (bool, error) { return result["old"] > 0, nil }).Should(BeTrue())
		}
	})

	ginkgo.It("shift 20% traffic to the new application for blue-green release", func() {
		host := "bg20.release.com"

		f.EnsureIngress(&v1beta1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      host,
				Namespace: f.Namespace,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/service-weight": fmt.Sprintf("%s: %d, %s: %d", oldNginxName, 20, newNginxName, 80),
				},
			},
			Spec: v1beta1.IngressSpec{
				Rules: []v1beta1.IngressRule{
					{
						Host: host,
						IngressRuleValue: v1beta1.IngressRuleValue{
							HTTP: &v1beta1.HTTPIngressRuleValue{
								Paths: []v1beta1.HTTPIngressPath{
									{
										Path: "/",
										Backend: v1beta1.IngressBackend{
											ServiceName: newNginxName,
											ServicePort: intstr.FromInt(nginxPort),
										},
									},
									{
										Path: "/",
										Backend: v1beta1.IngressBackend{
											ServiceName: oldNginxName,
											ServicePort: intstr.FromInt(nginxPort),
										},
									},
								},
							},
						},
					},
				},
			},
		})

		time.Sleep(waitForLuaSync)
		result := map[string]int{"old": 0, "new": 0}
		for index := 1; index <= 10; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				Expect()
			resp.Status(http.StatusOK)
			resl := strings.TrimSpace(resp.Body().Raw())
			result[resl] = result[resl] + 1
			time.Sleep(time.Second * 1)
		}

		// FIXME: You are trying to make an assertion, but Gomega's fail handler is nil
		if result["new"] <= 0 {
			Expect(func() (bool, error) { return result["new"] > 0, nil }).Should(BeTrue())
		}
		if result["old"] <= 0 {
			Expect(func() (bool, error) { return result["old"] > 0, nil }).Should(BeTrue())
		}
		if result["new"] <= result["old"] {
			Expect(func() (bool, error) { return result["new"] > result["old"], nil }).Should(BeTrue())
		}
	})

	ginkgo.It("allow the application's service port is different for blue-green release", func() {
		host := "bg60.port.release.com"

		f.EnsureIngress(&v1beta1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      host,
				Namespace: f.Namespace,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/service-weight": fmt.Sprintf("%s: %d, %s: %d", oldNginxName, 40, newTomcatName, 60),
				},
			},
			Spec: v1beta1.IngressSpec{
				Rules: []v1beta1.IngressRule{
					{
						Host: host,
						IngressRuleValue: v1beta1.IngressRuleValue{
							HTTP: &v1beta1.HTTPIngressRuleValue{
								Paths: []v1beta1.HTTPIngressPath{
									{
										Path: "/",
										Backend: v1beta1.IngressBackend{
											ServiceName: oldNginxName,
											ServicePort: intstr.FromInt(nginxPort),
										},
									},
									{
										Path: "/",
										Backend: v1beta1.IngressBackend{
											ServiceName: newTomcatName,
											ServicePort: intstr.FromInt(tomcatPort),
										},
									},
								},
							},
						},
					},
				},
			},
		})

		time.Sleep(waitForLuaSync)
		result := map[string]int{"old": 0, "tomcat": 0}
		for index := 1; index <= 10; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				Expect()
			resp.Status(http.StatusOK)
			resl := strings.TrimSpace(resp.Body().Raw())
			if strings.Contains(resl, "Tomcat") {
				resl = "tomcat"
			}
			result[resl] = result[resl] + 1
			time.Sleep(time.Second * 1)
		}
		if result["tomcat"] <= 0 {
			Expect(func() (bool, error) { return result["tomcat"] > 0, nil }).Should(BeTrue())
		}
		if result["old"] <= 0 {
			Expect(func() (bool, error) { return result["old"] > 0, nil }).Should(BeTrue())
		}
	})

	ginkgo.It("allow the backend service has no active endpoints for blue-green release", func() {
		host := "bg.endpoint.release.com"

		f.EnsureIngress(&v1beta1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      host,
				Namespace: f.Namespace,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/service-weight": fmt.Sprintf("%s: %d, %s: %d", oldNginxName, 50, newTomcatName, 50),
				},
			},
			Spec: v1beta1.IngressSpec{
				Rules: []v1beta1.IngressRule{
					{
						Host: host,
						IngressRuleValue: v1beta1.IngressRuleValue{
							HTTP: &v1beta1.HTTPIngressRuleValue{
								Paths: []v1beta1.HTTPIngressPath{
									{
										Path: "/",
										Backend: v1beta1.IngressBackend{
											ServiceName: oldNginxName,
											ServicePort: intstr.FromInt(nginxPort),
										},
									},
									{
										Path: "/",
										Backend: v1beta1.IngressBackend{
											ServiceName: newTomcatName,
											ServicePort: intstr.FromInt(tomcatPort),
										},
									},
								},
							},
						},
					},
				},
			},
		})

		// 1. nginx replicas = 0
		framework.UpdateDeployment(f.KubeClientSet, f.Namespace, oldNginxName, 0, nil)
		time.Sleep(waitForLuaSync)
		for index := 1; index <= 10; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				Expect()
			resp.Status(http.StatusOK)
			resp.Body().Contains("Tomcat")
			time.Sleep(time.Second * 1)
		}

		// 2. both replicas = 0
		framework.UpdateDeployment(f.KubeClientSet, f.Namespace, newTomcatName, 0, nil)
		time.Sleep(waitForLuaSync)
		for index := 1; index <= 10; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				Expect()
			resp.Status(http.StatusServiceUnavailable)
			time.Sleep(time.Second * 1)
		}

		// 3. tomcat replicas = 0
		framework.UpdateDeployment(f.KubeClientSet, f.Namespace, oldNginxName, 1, nil)
		time.Sleep(waitForLuaSync)
		for index := 1; index <= 10; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				Expect()
			resp.Status(http.StatusOK)
			resp.Body().Contains("old")
			time.Sleep(time.Second * 1)
		}
	})

	ginkgo.It("shift the request that contains Foo=bar in the header to the new application for gray release", func() {
		host := "header.release.com"

		f.EnsureIngress(&v1beta1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      host,
				Namespace: f.Namespace,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/service-match": fmt.Sprintf("%s: header(\"Foo\", /bar|rab/)", newNginxName),
				},
			},
			Spec: v1beta1.IngressSpec{
				Rules: []v1beta1.IngressRule{
					{
						Host: host,
						IngressRuleValue: v1beta1.IngressRuleValue{
							HTTP: &v1beta1.HTTPIngressRuleValue{
								Paths: []v1beta1.HTTPIngressPath{
									{
										Path: "/",
										Backend: v1beta1.IngressBackend{
											ServiceName: newNginxName,
											ServicePort: intstr.FromInt(nginxPort),
										},
									},
									{
										Path: "/",
										Backend: v1beta1.IngressBackend{
											ServiceName: oldNginxName,
											ServicePort: intstr.FromInt(nginxPort),
										},
									},
								},
							},
						},
					},
				},
			},
		})

		time.Sleep(waitForLuaSync)
		for index := 1; index <= 10; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				WithHeader("Foo", "bar").
				Expect()
			resp.Status(http.StatusOK)
			resp.Body().Contains("new")
			time.Sleep(time.Second * 1)
		}

		for index := 1; index <= 10; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				WithHeader("Foo", "baar").
				Expect()
			resp.Status(http.StatusOK)
			resp.Body().Contains("old")
			time.Sleep(time.Second * 1)
		}
	})

	ginkgo.It("shift the request that contains Foo=bar in the cookie to the new application for gray release", func() {
		host := "cookie.release.com"

		f.EnsureIngress(&v1beta1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      host,
				Namespace: f.Namespace,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/service-match": fmt.Sprintf("%s: cookie(\"Foo\", /bar|rab/)", newNginxName),
				},
			},
			Spec: v1beta1.IngressSpec{
				Rules: []v1beta1.IngressRule{
					{
						Host: host,
						IngressRuleValue: v1beta1.IngressRuleValue{
							HTTP: &v1beta1.HTTPIngressRuleValue{
								Paths: []v1beta1.HTTPIngressPath{
									{
										Path: "/",
										Backend: v1beta1.IngressBackend{
											ServiceName: newNginxName,
											ServicePort: intstr.FromInt(nginxPort),
										},
									},
									{
										Path: "/",
										Backend: v1beta1.IngressBackend{
											ServiceName: oldNginxName,
											ServicePort: intstr.FromInt(nginxPort),
										},
									},
								},
							},
						},
					},
				},
			},
		})

		time.Sleep(waitForLuaSync)
		for index := 1; index <= 10; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				WithCookies(map[string]string{"Foo": "rab"}).
				Expect()
			resp.Status(http.StatusOK)
			resp.Body().Contains("new")
			time.Sleep(time.Second * 1)
		}

		for index := 1; index <= 10; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				WithCookies(map[string]string{"Foo": "raab"}).
				Expect()
			resp.Status(http.StatusOK)
			resp.Body().Contains("old")
			time.Sleep(time.Second * 1)
		}
	})

	ginkgo.It("shift the request that contains foo=bar in the query parameter to the new application for gray release", func() {
		host := "query.release.com"

		f.EnsureIngress(&v1beta1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      host,
				Namespace: f.Namespace,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/service-match": fmt.Sprintf("%s: query(\"foo\", /bar|rab/)", newNginxName),
				},
			},
			Spec: v1beta1.IngressSpec{
				Rules: []v1beta1.IngressRule{
					{
						Host: host,
						IngressRuleValue: v1beta1.IngressRuleValue{
							HTTP: &v1beta1.HTTPIngressRuleValue{
								Paths: []v1beta1.HTTPIngressPath{
									{
										Path: "/",
										Backend: v1beta1.IngressBackend{
											ServiceName: oldNginxName,
											ServicePort: intstr.FromInt(nginxPort),
										},
									},
									{
										Path: "/",
										Backend: v1beta1.IngressBackend{
											ServiceName: newNginxName,
											ServicePort: intstr.FromInt(nginxPort),
										},
									},
								},
							},
						},
					},
				},
			},
		})

		time.Sleep(waitForLuaSync)
		for index := 1; index <= 10; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				WithQuery("foo", "bar").
				Expect()
			resp.Status(http.StatusOK)
			resp.Body().Contains("new")
			time.Sleep(time.Second * 1)
		}

		for index := 1; index <= 10; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				WithQuery("foo", "baar").
				Expect()
			resp.Status(http.StatusOK)
			resp.Body().Contains("old")
			time.Sleep(time.Second * 1)
		}
	})

	ginkgo.It("allow the application's service port is different for gray release", func() {
		host := "header.port.release.com"

		f.EnsureIngress(&v1beta1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      host,
				Namespace: f.Namespace,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/service-match": fmt.Sprintf("%s: header(\"App\", \"tomcat\")", newTomcatName),
				},
			},
			Spec: v1beta1.IngressSpec{
				Rules: []v1beta1.IngressRule{
					{
						Host: host,
						IngressRuleValue: v1beta1.IngressRuleValue{
							HTTP: &v1beta1.HTTPIngressRuleValue{
								Paths: []v1beta1.HTTPIngressPath{
									{
										Path: "/",
										Backend: v1beta1.IngressBackend{
											ServiceName: oldNginxName,
											ServicePort: intstr.FromInt(nginxPort),
										},
									},
									{
										Path: "/",
										Backend: v1beta1.IngressBackend{
											ServiceName: newTomcatName,
											ServicePort: intstr.FromInt(tomcatPort),
										},
									},
								},
							},
						},
					},
				},
			},
		})

		time.Sleep(waitForLuaSync)
		for index := 1; index <= 10; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				WithHeader("App", "tomcat").
				Expect()
			resp.Status(http.StatusOK)
			resp.Body().Contains("Tomcat")
			time.Sleep(time.Second * 1)
		}

		for index := 1; index <= 10; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				Expect()
			resp.Status(http.StatusOK)
			resp.Body().Contains("old")
			time.Sleep(time.Second * 1)
		}
	})

	ginkgo.It("1. allow the backend service has no active endpoints for gray release", func() {
		host := "gray.endpoint.release.com"

		f.EnsureIngress(&v1beta1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      host,
				Namespace: f.Namespace,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/service-match": fmt.Sprintf("%s: header(\"Foo\", \"bar\")", oldNginxName),
				},
			},
			Spec: v1beta1.IngressSpec{
				Rules: []v1beta1.IngressRule{
					{
						Host: host,
						IngressRuleValue: v1beta1.IngressRuleValue{
							HTTP: &v1beta1.HTTPIngressRuleValue{
								Paths: []v1beta1.HTTPIngressPath{
									{
										Path: "/",
										Backend: v1beta1.IngressBackend{
											ServiceName: oldNginxName,
											ServicePort: intstr.FromInt(nginxPort),
										},
									},
									{
										Path: "/",
										Backend: v1beta1.IngressBackend{
											ServiceName: newNginxName,
											ServicePort: intstr.FromInt(nginxPort),
										},
									},
								},
							},
						},
					},
				},
			},
		})

		// 1. old-nginx replicas = 0
		framework.UpdateDeployment(f.KubeClientSet, f.Namespace, oldNginxName, 0, nil)
		time.Sleep(waitForLuaSync)
		for index := 1; index <= 10; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				WithHeader("Foo", "bar").
				Expect()
			resp.Status(http.StatusOK)
			resp.Body().Contains("new")
			time.Sleep(time.Second * 1)
		}

		// 2. both replicas = 0
		framework.UpdateDeployment(f.KubeClientSet, f.Namespace, newNginxName, 0, nil)
		time.Sleep(waitForLuaSync)
		for index := 1; index <= 10; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				WithHeader("Foo", "bar").
				Expect()
			resp.Status(http.StatusServiceUnavailable)
			time.Sleep(time.Second * 1)
		}

		// 3. new-nginx replicas = 0
		framework.UpdateDeployment(f.KubeClientSet, f.Namespace, oldNginxName, 1, nil)
		time.Sleep(waitForLuaSync)
		for index := 1; index <= 10; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				WithHeader("Foo", "bar").
				Expect()
			resp.Status(http.StatusOK)
			resp.Body().Contains("old")
			time.Sleep(time.Second * 1)
		}
	})

	ginkgo.It("2. allow the backend service has no active endpoints for gray release", func() {
		host := "gray.endpoint.release.com"

		f.EnsureIngress(&v1beta1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      host,
				Namespace: f.Namespace,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/service-match": fmt.Sprintf("%s: header(\"Foo\", \"bar\")", newNginxName),
				},
			},
			Spec: v1beta1.IngressSpec{
				Rules: []v1beta1.IngressRule{
					{
						Host: host,
						IngressRuleValue: v1beta1.IngressRuleValue{
							HTTP: &v1beta1.HTTPIngressRuleValue{
								Paths: []v1beta1.HTTPIngressPath{
									{
										Path: "/",
										Backend: v1beta1.IngressBackend{
											ServiceName: oldNginxName,
											ServicePort: intstr.FromInt(nginxPort),
										},
									},
									{
										Path: "/",
										Backend: v1beta1.IngressBackend{
											ServiceName: newNginxName,
											ServicePort: intstr.FromInt(nginxPort),
										},
									},
								},
							},
						},
					},
				},
			},
		})

		// 1. old-nginx replicas = 0
		framework.UpdateDeployment(f.KubeClientSet, f.Namespace, oldNginxName, 0, nil)
		time.Sleep(waitForLuaSync)
		for index := 1; index <= 10; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				WithHeader("Foo", "bar").
				Expect()
			resp.Status(http.StatusOK)
			resp.Body().Contains("new")
			time.Sleep(time.Second * 1)
		}

		// 2. both replicas = 0
		framework.UpdateDeployment(f.KubeClientSet, f.Namespace, newNginxName, 0, nil)
		time.Sleep(waitForLuaSync)
		for index := 1; index <= 10; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				WithHeader("Foo", "bar").
				Expect()
			resp.Status(http.StatusServiceUnavailable)
			time.Sleep(time.Second * 1)
		}

		// 3. new-nginx replicas = 0
		framework.UpdateDeployment(f.KubeClientSet, f.Namespace, oldNginxName, 1, nil)
		time.Sleep(waitForLuaSync)
		for index := 1; index <= 10; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				WithHeader("Foo", "bar").
				Expect()
			resp.Status(http.StatusOK)
			resp.Body().Contains("old")
			time.Sleep(time.Second * 1)
		}
	})

	ginkgo.It("both setting service-match and service-weight for gray release", func() {
		host := "smsw.release.com"

		f.EnsureIngress(&v1beta1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      host,
				Namespace: f.Namespace,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/service-match":  fmt.Sprintf("%s: header(\"App\", \"tomcat\")", newTomcatName),
					"nginx.ingress.kubernetes.io/service-weight": fmt.Sprintf("%s: %d, %s: %d", oldNginxName, 50, newTomcatName, 50),
				},
			},
			Spec: v1beta1.IngressSpec{
				Rules: []v1beta1.IngressRule{
					{
						Host: host,
						IngressRuleValue: v1beta1.IngressRuleValue{
							HTTP: &v1beta1.HTTPIngressRuleValue{
								Paths: []v1beta1.HTTPIngressPath{
									{
										Path: "/",
										Backend: v1beta1.IngressBackend{
											ServiceName: oldNginxName,
											ServicePort: intstr.FromInt(nginxPort),
										},
									},
									{
										Path: "/",
										Backend: v1beta1.IngressBackend{
											ServiceName: newTomcatName,
											ServicePort: intstr.FromInt(tomcatPort),
										},
									},
								},
							},
						},
					},
				},
			},
		})

		time.Sleep(waitForLuaSync)
		result := map[string]int{"old": 0, "tomcat": 0}
		for index := 1; index <= 10; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				WithHeader("App", "tomcat").
				Expect()
			resp.Status(http.StatusOK)
			resl := strings.TrimSpace(resp.Body().Raw())
			if strings.Contains(resl, "Tomcat") {
				resl = "tomcat"
			}
			result[resl] = result[resl] + 1
			time.Sleep(time.Second * 1)
		}
		if result["tomcat"] <= 0 {
			Expect(func() (bool, error) { return result["tomcat"] > 0, nil }).Should(BeTrue())
		}
		if result["old"] <= 0 {
			Expect(func() (bool, error) { return result["old"] > 0, nil }).Should(BeTrue())
		}

		for index := 1; index <= 10; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				Expect()
			resp.Status(http.StatusOK)
			resp.Body().Contains("old")
			time.Sleep(time.Second * 1)
		}
	})
})
