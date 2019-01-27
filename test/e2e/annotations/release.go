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
	"github.com/parnurzeal/gorequest"
	"strings"
	"time"

	"io/ioutil"
	"net/http"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"k8s.io/api/extensions/v1beta1"
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
)

var _ = framework.IngressNginxDescribe("Annotations - Release", func() {
	f := framework.NewDefaultFramework("release")

	BeforeEach(func() {
		f.NewDeployment(oldNginxName, oldNginxImage, nginxPort, 1)
		f.NewDeployment(newNginxName, newNginxImage, nginxPort, 1)
		f.NewDeployment(newTomcatName, newTomcatImage, tomcatPort, 1)
	})

	Context("when release by service-weight", func() {
		It("shift 50% traffic to the new application for blue-green release", func() {
			host := "bg50.release.com"

			f.EnsureIngress(&v1beta1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      host,
					Namespace: f.IngressController.Namespace,
					Annotations: map[string]string{
						"nginx.ingress.kubernetes.io/service-weight":
						fmt.Sprintf("%s: %d, %s: %d", oldNginxName, 50, newNginxName, 50),
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
				resp, _, errs := gorequest.New().
					SetDebug(true).
					SetCurlCommand(true).
					Get(f.IngressController.HTTPURL).
					Set("Host", host).
					End()

				Expect(len(errs)).Should(BeNumerically("==", 0))
				Expect(resp.StatusCode).Should(Equal(http.StatusOK))

				body, _ := ioutil.ReadAll(resp.Body)
				resl := strings.TrimSpace(string(body))
				result[resl] = result[resl] + 1
				time.Sleep(time.Second * 1)
			}

			framework.Logf("Call result: " + fmt.Sprintf("%v", result))
			Expect(result["new"] > 0).Should(BeTrue())
			Expect(result["old"] > 0).Should(BeTrue())
		})

		It("shift 20% traffic to the new application for blue-green release", func() {
			host := "bg20.release.com"

			f.EnsureIngress(&v1beta1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      host,
					Namespace: f.IngressController.Namespace,
					Annotations: map[string]string{
						"nginx.ingress.kubernetes.io/service-weight":
						fmt.Sprintf("%s: %d, %s: %d", oldNginxName, 20, newNginxName, 80),
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
				resp, _, errs := gorequest.New().
					SetDebug(true).
					SetCurlCommand(true).
					Get(f.IngressController.HTTPURL).
					Set("Host", host).
					End()

				Expect(len(errs)).Should(BeNumerically("==", 0))
				Expect(resp.StatusCode).Should(Equal(http.StatusOK))

				body, _ := ioutil.ReadAll(resp.Body)
				resl := strings.TrimSpace(string(body))
				result[resl] = result[resl] + 1
				time.Sleep(time.Second * 1)
			}

			framework.Logf("Call result: " + fmt.Sprintf("%v", result))
			Expect(result["new"] > 0).Should(BeTrue())
			Expect(result["old"] > 0).Should(BeTrue())
			Expect(result["new"] > result["old"]).Should(BeTrue())
		})

		It("allow the application's service port is different for blue-green release", func() {
			host := "bg60.port.release.com"

			f.EnsureIngress(&v1beta1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      host,
					Namespace: f.IngressController.Namespace,
					Annotations: map[string]string{
						"nginx.ingress.kubernetes.io/service-weight":
						fmt.Sprintf("%s: %d, %s: %d", oldNginxName, 40, newTomcatName, 60),
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
				resp, _, errs := gorequest.New().
					SetDebug(true).
					SetCurlCommand(true).
					Get(f.IngressController.HTTPURL).
					Set("Host", host).
					End()

				Expect(len(errs)).Should(BeNumerically("==", 0))
				Expect(resp.StatusCode).Should(Equal(http.StatusOK))

				body, _ := ioutil.ReadAll(resp.Body)
				resl := strings.TrimSpace(string(body))
				if strings.Contains(resl, "Tomcat") {
					resl = "tomcat"
				}
				result[resl] = result[resl] + 1
				time.Sleep(time.Second * 1)
			}

			framework.Logf("Call result: " + fmt.Sprintf("%v", result))
			Expect(result["tomcat"] > 0).Should(BeTrue())
			Expect(result["old"] > 0).Should(BeTrue())
		})

		It("allow the backend service has no active endpoints for blue-green release", func() {
			host := "bg.endpoint.release.com"

			f.EnsureIngress(&v1beta1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      host,
					Namespace: f.IngressController.Namespace,
					Annotations: map[string]string{
						"nginx.ingress.kubernetes.io/service-weight":
						fmt.Sprintf("%s: %d, %s: %d", oldNginxName, 50, newTomcatName, 50),
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
			err := framework.UpdateDeployment(f.KubeClientSet, f.IngressController.Namespace, oldNginxName,
				0, nil)
			Expect(err).NotTo(HaveOccurred())

			time.Sleep(waitForLuaSync)
			for index := 1; index <= 10; index++ {
				resp, _, errs := gorequest.New().
					SetDebug(true).
					SetCurlCommand(true).
					Get(f.IngressController.HTTPURL).
					Set("Host", host).
					End()
				Expect(len(errs)).Should(BeNumerically("==", 0))
				Expect(resp.StatusCode).Should(Equal(http.StatusOK))

				body, _ := ioutil.ReadAll(resp.Body)
				resl := strings.TrimSpace(string(body))
				Expect(strings.Contains(resl, "Tomcat")).Should(BeTrue())

				time.Sleep(time.Second * 1)
			}

			// 2. both replicas = 0
			err = framework.UpdateDeployment(f.KubeClientSet, f.IngressController.Namespace, newTomcatName,
				0, nil)
			Expect(err).NotTo(HaveOccurred())

			time.Sleep(waitForLuaSync)
			for index := 1; index <= 10; index++ {
				resp, _, errs := gorequest.New().
					SetDebug(true).
					SetCurlCommand(true).
					Get(f.IngressController.HTTPURL).
					Set("Host", host).
					End()
				Expect(len(errs)).Should(BeNumerically("==", 0))
				Expect(resp.StatusCode).Should(Equal(http.StatusServiceUnavailable))

				time.Sleep(time.Second * 1)
			}

			// 3. tomcat replicas = 0
			err = framework.UpdateDeployment(f.KubeClientSet, f.IngressController.Namespace, oldNginxName,
				1, nil)
			Expect(err).NotTo(HaveOccurred())

			time.Sleep(waitForLuaSync)
			for index := 1; index <= 10; index++ {
				resp, _, errs := gorequest.New().
					SetDebug(true).
					SetCurlCommand(true).
					Get(f.IngressController.HTTPURL).
					Set("Host", host).
					End()
				Expect(len(errs)).Should(BeNumerically("==", 0))
				Expect(resp.StatusCode).Should(Equal(http.StatusOK))

				body, _ := ioutil.ReadAll(resp.Body)
				resl := strings.TrimSpace(string(body))
				Expect(resl == "old").Should(BeTrue())

				time.Sleep(time.Second * 1)
			}
		})
	})

	Context("when release by service-match", func() {
		It("shift the request that contains Foo=bar in the header to the new application for gray release", func() {
			host := "header.release.com"

			f.EnsureIngress(&v1beta1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      host,
					Namespace: f.IngressController.Namespace,
					Annotations: map[string]string{
						"nginx.ingress.kubernetes.io/service-match":
						fmt.Sprintf("%s: header(\"Foo\", /bar|rab/)", newNginxName),
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
				resp, _, errs := gorequest.New().
					SetDebug(true).
					SetCurlCommand(true).
					Get(f.IngressController.HTTPURL).
					Set("Host", host).
					Set("Foo", "bar").
					End()

				body, _ := ioutil.ReadAll(resp.Body)
				resl := strings.TrimSpace(string(body))
				Expect(len(errs)).Should(BeNumerically("==", 0))
				Expect(resp.StatusCode).Should(Equal(http.StatusOK))
				Expect(resl).Should(Equal("new"))

				time.Sleep(time.Second * 1)
			}

			for index := 1; index <= 10; index++ {
				resp, _, errs := gorequest.New().
					SetDebug(true).
					SetCurlCommand(true).
					Get(f.IngressController.HTTPURL).
					Set("Host", host).
					Set("Foo", "baar").
					End()

				body, _ := ioutil.ReadAll(resp.Body)
				resl := strings.TrimSpace(string(body))
				Expect(len(errs)).Should(BeNumerically("==", 0))
				Expect(resp.StatusCode).Should(Equal(http.StatusOK))
				Expect(resl).Should(Equal("old"))

				time.Sleep(time.Second * 1)
			}
		})

		It("shift the request that contains Foo=bar in the cookie to the new application for gray release", func() {
			host := "cookie.release.com"

			f.EnsureIngress(&v1beta1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      host,
					Namespace: f.IngressController.Namespace,
					Annotations: map[string]string{
						"nginx.ingress.kubernetes.io/service-match":
						fmt.Sprintf("%s: cookie(\"Foo\", /bar|rab/)", newNginxName),
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
				resp, _, errs := gorequest.New().
					Get(f.IngressController.HTTPURL).
					SetDebug(true).
					SetCurlCommand(true).
					Set("Host", host).
					AddCookie(&http.Cookie{Name: "Foo", Value: "rab"}).
					End()

				body, _ := ioutil.ReadAll(resp.Body)
				resl := strings.TrimSpace(string(body))
				Expect(len(errs)).Should(BeNumerically("==", 0))
				Expect(resp.StatusCode).Should(Equal(http.StatusOK))
				Expect(resl).Should(Equal("new"))

				time.Sleep(time.Second * 1)
			}

			for index := 1; index <= 10; index++ {
				resp, _, errs := gorequest.New().
					Get(f.IngressController.HTTPURL).
					SetDebug(true).
					SetCurlCommand(true).
					Set("Host", host).
					AddCookie(&http.Cookie{Name: "Foo", Value: "raab"}).
					End()

				body, _ := ioutil.ReadAll(resp.Body)
				resl := strings.TrimSpace(string(body))
				Expect(len(errs)).Should(BeNumerically("==", 0))
				Expect(resp.StatusCode).Should(Equal(http.StatusOK))
				Expect(resl).Should(Equal("old"))

				time.Sleep(time.Second * 1)
			}
		})

		It("shift the request that contains foo=bar in the query parameter to the new application for gray release", func() {
			host := "query.release.com"

			f.EnsureIngress(&v1beta1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      host,
					Namespace: f.IngressController.Namespace,
					Annotations: map[string]string{
						"nginx.ingress.kubernetes.io/service-match":
						fmt.Sprintf("%s: query(\"foo\", /bar|rab/)", newNginxName),
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
				resp, _, errs := gorequest.New().
					Get(f.IngressController.HTTPURL).
					SetDebug(true).
					SetCurlCommand(true).
					Set("Host", host).
					Query("foo=bar").
					End()

				body, _ := ioutil.ReadAll(resp.Body)
				resl := strings.TrimSpace(string(body))
				Expect(len(errs)).Should(BeNumerically("==", 0))
				Expect(resp.StatusCode).Should(Equal(http.StatusOK))
				Expect(resl).Should(Equal("new"))

				time.Sleep(time.Second * 1)
			}

			for index := 1; index <= 10; index++ {
				resp, _, errs := gorequest.New().
					Get(f.IngressController.HTTPURL).
					SetDebug(true).
					SetCurlCommand(true).
					Set("Host", host).
					Query("foo=baar").
					End()

				body, _ := ioutil.ReadAll(resp.Body)
				resl := strings.TrimSpace(string(body))
				Expect(len(errs)).Should(BeNumerically("==", 0))
				Expect(resp.StatusCode).Should(Equal(http.StatusOK))
				Expect(resl).Should(Equal("old"))

				time.Sleep(time.Second * 1)
			}
		})

		It("allow the application's service port is different for gray release", func() {
			host := "header.port.release.com"

			f.EnsureIngress(&v1beta1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      host,
					Namespace: f.IngressController.Namespace,
					Annotations: map[string]string{
						"nginx.ingress.kubernetes.io/service-match":
						fmt.Sprintf("%s: header(\"App\", \"tomcat\")", newTomcatName),
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
				resp, _, errs := gorequest.New().
					SetDebug(true).
					SetCurlCommand(true).
					Get(f.IngressController.HTTPURL).
					Set("Host", host).
					Set("App", "tomcat").
					End()

				body, _ := ioutil.ReadAll(resp.Body)
				resl := strings.TrimSpace(string(body))
				Expect(len(errs)).Should(BeNumerically("==", 0))
				Expect(resp.StatusCode).Should(Equal(http.StatusOK))
				Expect(strings.Contains(resl, "Tomcat")).Should(BeTrue())

				time.Sleep(time.Second * 1)
			}

			for index := 1; index <= 10; index++ {
				resp, _, errs := gorequest.New().
					SetDebug(true).
					SetCurlCommand(true).
					Get(f.IngressController.HTTPURL).
					Set("Host", host).
					End()

				body, _ := ioutil.ReadAll(resp.Body)
				resl := strings.TrimSpace(string(body))
				Expect(len(errs)).Should(BeNumerically("==", 0))
				Expect(resp.StatusCode).Should(Equal(http.StatusOK))
				Expect(resl).Should(Equal("old"))

				time.Sleep(time.Second * 1)
			}
		})

		It("1. allow the backend service has no active endpoints for gray release", func() {
			host := "gray.endpoint.release.com"

			f.EnsureIngress(&v1beta1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      host,
					Namespace: f.IngressController.Namespace,
					Annotations: map[string]string{
						"nginx.ingress.kubernetes.io/service-match":
						fmt.Sprintf("%s: header(\"Foo\", \"bar\")", oldNginxName),
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
			err := framework.UpdateDeployment(f.KubeClientSet, f.IngressController.Namespace, oldNginxName,
				0, nil)
			Expect(err).NotTo(HaveOccurred())

			time.Sleep(waitForLuaSync)
			for index := 1; index <= 10; index++ {
				resp, _, errs := gorequest.New().
					SetDebug(true).
					SetCurlCommand(true).
					Get(f.IngressController.HTTPURL).
					Set("Host", host).
					Set("Foo", "bar").
					End()

				body, _ := ioutil.ReadAll(resp.Body)
				resl := strings.TrimSpace(string(body))
				Expect(len(errs)).Should(BeNumerically("==", 0))
				Expect(resp.StatusCode).Should(Equal(http.StatusOK))
				Expect(resl).Should(Equal("new"))

				time.Sleep(time.Second * 1)
			}

			// 2. both replicas = 0
			err = framework.UpdateDeployment(f.KubeClientSet, f.IngressController.Namespace, newNginxName,
				0, nil)
			Expect(err).NotTo(HaveOccurred())

			time.Sleep(waitForLuaSync)
			for index := 1; index <= 10; index++ {
				resp, _, errs := gorequest.New().
					SetDebug(true).
					SetCurlCommand(true).
					Get(f.IngressController.HTTPURL).
					Set("Host", host).
					Set("Foo", "bar").
					End()
				Expect(len(errs)).Should(BeNumerically("==", 0))
				Expect(resp.StatusCode).Should(Equal(http.StatusServiceUnavailable))

				time.Sleep(time.Second * 1)
			}

			// 3. new-nginx replicas = 0
			err = framework.UpdateDeployment(f.KubeClientSet, f.IngressController.Namespace, oldNginxName,
				1, nil)
			Expect(err).NotTo(HaveOccurred())

			time.Sleep(waitForLuaSync)
			for index := 1; index <= 10; index++ {
				resp, _, errs := gorequest.New().
					SetDebug(true).
					SetCurlCommand(true).
					Get(f.IngressController.HTTPURL).
					Set("Host", host).
					Set("Foo", "bar").
					End()

				body, _ := ioutil.ReadAll(resp.Body)
				resl := strings.TrimSpace(string(body))
				Expect(len(errs)).Should(BeNumerically("==", 0))
				Expect(resp.StatusCode).Should(Equal(http.StatusOK))
				Expect(resl).Should(Equal("old"))

				time.Sleep(time.Second * 1)
			}
		})

		It("2. allow the backend service has no active endpoints for gray release", func() {
			host := "gray.endpoint.release.com"

			f.EnsureIngress(&v1beta1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      host,
					Namespace: f.IngressController.Namespace,
					Annotations: map[string]string{
						"nginx.ingress.kubernetes.io/service-match":
						fmt.Sprintf("%s: header(\"Foo\", \"bar\")", newNginxName),
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
			err := framework.UpdateDeployment(f.KubeClientSet, f.IngressController.Namespace, oldNginxName,
				0, nil)
			Expect(err).NotTo(HaveOccurred())

			time.Sleep(waitForLuaSync)
			for index := 1; index <= 10; index++ {
				resp, _, errs := gorequest.New().
					SetDebug(true).
					SetCurlCommand(true).
					Get(f.IngressController.HTTPURL).
					Set("Host", host).
					Set("Foo", "bar").
					End()

				body, _ := ioutil.ReadAll(resp.Body)
				resl := strings.TrimSpace(string(body))
				Expect(len(errs)).Should(BeNumerically("==", 0))
				Expect(resp.StatusCode).Should(Equal(http.StatusOK))
				Expect(resl).Should(Equal("new"))

				time.Sleep(time.Second * 1)
			}

			// 2. both replicas = 0
			err = framework.UpdateDeployment(f.KubeClientSet, f.IngressController.Namespace, newNginxName,
				0, nil)
			Expect(err).NotTo(HaveOccurred())

			time.Sleep(waitForLuaSync)
			for index := 1; index <= 10; index++ {
				resp, _, errs := gorequest.New().
					SetDebug(true).
					SetCurlCommand(true).
					Get(f.IngressController.HTTPURL).
					Set("Host", host).
					Set("Foo", "bar").
					End()
				Expect(len(errs)).Should(BeNumerically("==", 0))
				Expect(resp.StatusCode).Should(Equal(http.StatusServiceUnavailable))

				time.Sleep(time.Second * 1)
			}

			// 3. new-nginx replicas = 0
			err = framework.UpdateDeployment(f.KubeClientSet, f.IngressController.Namespace, oldNginxName,
				1, nil)
			Expect(err).NotTo(HaveOccurred())

			time.Sleep(waitForLuaSync)
			for index := 1; index <= 10; index++ {
				resp, _, errs := gorequest.New().
					SetDebug(true).
					SetCurlCommand(true).
					Get(f.IngressController.HTTPURL).
					Set("Host", host).
					Set("Foo", "bar").
					End()

				body, _ := ioutil.ReadAll(resp.Body)
				resl := strings.TrimSpace(string(body))
				Expect(len(errs)).Should(BeNumerically("==", 0))
				Expect(resp.StatusCode).Should(Equal(http.StatusOK))
				Expect(resl).Should(Equal("old"))

				time.Sleep(time.Second * 1)
			}
		})
	})

	Context("when release by service-match and service-weight", func() {
		It("both setting service-match and service-weight for gray release", func() {
			host := "smsw.release.com"

			f.EnsureIngress(&v1beta1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      host,
					Namespace: f.IngressController.Namespace,
					Annotations: map[string]string{
						"nginx.ingress.kubernetes.io/service-match":
						fmt.Sprintf("%s: header(\"App\", \"tomcat\")", newTomcatName),
						"nginx.ingress.kubernetes.io/service-weight":
						fmt.Sprintf("%s: %d, %s: %d", oldNginxName, 50, newTomcatName, 50),
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
				resp, _, errs := gorequest.New().
					SetDebug(true).
					SetCurlCommand(true).
					Get(f.IngressController.HTTPURL).
					Set("Host", host).
					Set("App", "tomcat").
					End()

				Expect(len(errs)).Should(BeNumerically("==", 0))
				Expect(resp.StatusCode).Should(Equal(http.StatusOK))

				body, _ := ioutil.ReadAll(resp.Body)
				resl := strings.TrimSpace(string(body))
				if strings.Contains(resl, "Tomcat") {
					resl = "tomcat"
				}
				result[resl] = result[resl] + 1

				time.Sleep(time.Second * 1)
			}

			framework.Logf("Call result: " + fmt.Sprintf("%v", result))
			Expect(result["tomcat"] > 0).Should(BeTrue())
			Expect(result["old"] > 0).Should(BeTrue())

			for index := 1; index <= 10; index++ {
				resp, _, errs := gorequest.New().
					SetDebug(true).
					SetCurlCommand(true).
					Get(f.IngressController.HTTPURL).
					Set("Host", host).
					End()

				body, _ := ioutil.ReadAll(resp.Body)
				resl := strings.TrimSpace(string(body))
				Expect(len(errs)).Should(BeNumerically("==", 0))
				Expect(resp.StatusCode).Should(Equal(http.StatusOK))
				Expect(resl).Should(Equal("old"))

				time.Sleep(time.Second * 1)
			}
		})
	})
})
