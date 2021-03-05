package lua

import (
	"strings"

	"github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"net/http"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/api/networking/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/ingress-nginx/test/e2e/framework"
)

const (
	nginxPort     = 80
	oldNginxName  = "old-nginx"
	newNginxName  = "new-nginx"
	oldNginxImage = "registry.cn-hangzhou.aliyuncs.com/acs-sample/old-nginx"
	newNginxImage = "registry.cn-hangzhou.aliyuncs.com/acs-sample/new-nginx"
)

var _ = framework.DescribeAnnotation("Dynamic Server", func() {
	f := framework.NewDefaultFramework("dynamic-server")

	ginkgo.BeforeEach(func() {
		framework.UpdateDeployment(f.KubeClientSet, f.Namespace, "nginx-ingress-controller", 1,
			func(deployment *appsv1.Deployment) error {
				args := deployment.Spec.Template.Spec.Containers[0].Args
				args = append(args, "--enable-dynamic-servers=true")
				deployment.Spec.Template.Spec.Containers[0].Args = args
				_, err := f.KubeClientSet.AppsV1().Deployments(f.Namespace).Update(deployment)
				return err
			})

		f.WaitForNginxConfiguration(
			func(cfg string) bool {
				return strings.Contains(cfg, "ok, res = pcall(require, \"certificate\")") &&
					strings.Contains(cfg, "balancer.rewrite()")
			})

		f.NewDeployment(oldNginxName, oldNginxImage, nginxPort, 1)
		f.NewDeployment(newNginxName, newNginxImage, nginxPort, 1)
	})

	ginkgo.It("Create a new ingress without reloading", func() {
		host := "dus.foo.com"

		var nginxConfig string
		f.WaitForNginxConfiguration(func(cfg string) bool {
			nginxConfig = cfg
			return true
		})

		// should return 404
		for index := 1; index <= 5; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				Expect()
			resp.Status(http.StatusNotFound)
			time.Sleep(time.Second * 1)
		}

		ginkgo.By("Create a new ingress without reloading")
		f.EnsureIngress(&v1beta1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      host,
				Namespace: f.Namespace,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/ssl-redirect": "false",
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
								},
							},
						},
					},
				},
			},
		})

		time.Sleep(waitForLuaSync)
		var newNginxConfig string
		f.WaitForNginxConfiguration(func(cfg string) bool {
			newNginxConfig = cfg
			return true
		})
		if nginxConfig != newNginxConfig {
			Expect(func() (string, error) {
				return nginxConfig, nil
			}).Should(Equal(newNginxConfig))
		}

		// should return 200
		for index := 1; index <= 5; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				Expect()
			resp.Status(http.StatusOK)
			resp.Body().Contains("old")
			time.Sleep(time.Second * 1)
		}
	})

	ginkgo.It("Support wildcard hostname", func() {
		host := "wc.bar.com"

		ginkgo.By("Create a ingress with wildcard hostname")
		f.EnsureIngress(&v1beta1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      host,
				Namespace: f.Namespace,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/ssl-redirect": "false",
				},
			},
			Spec: v1beta1.IngressSpec{
				Rules: []v1beta1.IngressRule{
					{
						Host: "*.bar.com",
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
								},
							},
						},
					},
				},
			},
		})

		time.Sleep(waitForLuaSync)
		for index := 1; index <= 5; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				Expect()
			resp.Status(http.StatusOK)
			resp.Body().Contains("old")
			time.Sleep(time.Second * 1)
		}
	})

	ginkgo.It("Use the longest location path", func() {
		host := "longest.bar.com"

		ginkgo.By("Create a ingress with multiple path services")
		f.EnsureIngress(&v1beta1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      host,
				Namespace: f.Namespace,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/ssl-redirect": "false",
				},
			},
			Spec: v1beta1.IngressSpec{
				Rules: []v1beta1.IngressRule{
					{
						Host: "*.bar.com",
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
										Path: "/longest",
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
		for index := 1; index <= 5; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				Expect()
			resp.Status(http.StatusOK)
			resp.Body().Contains("old")
			time.Sleep(time.Second * 1)
		}

		for index := 1; index <= 5; index++ {
			resp := f.HTTPTestClient().
				GET("/longest").
				WithHeader("Host", host).
				Expect()
			resp.Status(http.StatusNotFound)
			time.Sleep(time.Second * 1)
		}
	})

	ginkgo.It("Support ip white list annotation", func() {
		host := "ipw.bar.com"

		ginkgo.By("Create a ingress with ip white list")
		f.EnsureIngress(&v1beta1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      host,
				Namespace: f.Namespace,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/ssl-redirect":           "false",
					"nginx.ingress.kubernetes.io/whitelist-source-range": "100.100.100.0/24,127.0.0.1",
				},
			},
			Spec: v1beta1.IngressSpec{
				Rules: []v1beta1.IngressRule{
					{
						Host: "*.bar.com",
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
								},
							},
						},
					},
				},
			},
		})

		time.Sleep(waitForLuaSync)
		for index := 1; index <= 5; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				Expect()
			resp.Status(http.StatusForbidden)
			time.Sleep(time.Second * 1)
		}

		ginkgo.By("Update ingress without ip white list")
		f.UpdateIngress(&v1beta1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      host,
				Namespace: f.Namespace,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/ssl-redirect": "false",
				},
			},
			Spec: v1beta1.IngressSpec{
				Rules: []v1beta1.IngressRule{
					{
						Host: "*.bar.com",
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
								},
							},
						},
					},
				},
			},
		})

		time.Sleep(waitForLuaSync)
		for index := 1; index <= 5; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				Expect()
			resp.Status(http.StatusOK)
			resp.Body().Contains("old")
			time.Sleep(time.Second * 1)
		}
	})

	ginkgo.It("Support redirect annotation", func() {
		host := "redirect.bar.com"

		ginkgo.By("Create a ingress with 308 permanent redirect")
		f.EnsureIngress(&v1beta1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      host,
				Namespace: f.Namespace,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/ssl-redirect":            "false",
					"nginx.ingress.kubernetes.io/permanent-redirect":      "https://www.aliyun.com",
					"nginx.ingress.kubernetes.io/permanent-redirect-code": "308",
				},
			},
			Spec: v1beta1.IngressSpec{
				Rules: []v1beta1.IngressRule{
					{
						Host: "*.bar.com",
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
								},
							},
						},
					},
				},
			},
		})

		time.Sleep(waitForLuaSync)
		for index := 1; index <= 5; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				Expect()
			resp.Status(http.StatusPermanentRedirect)
			time.Sleep(time.Second * 1)
		}

		ginkgo.By("Update ingress without 308 permanent redirect")
		f.UpdateIngress(&v1beta1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      host,
				Namespace: f.Namespace,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/ssl-redirect": "false",
				},
			},
			Spec: v1beta1.IngressSpec{
				Rules: []v1beta1.IngressRule{
					{
						Host: "*.bar.com",
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
								},
							},
						},
					},
				},
			},
		})

		time.Sleep(waitForLuaSync)
		for index := 1; index <= 5; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				Expect()
			resp.Status(http.StatusOK)
			resp.Body().Contains("old")
			time.Sleep(time.Second * 1)
		}

		ginkgo.By("Create a ingress with 302 temporal redirect")
		f.UpdateIngress(&v1beta1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      host,
				Namespace: f.Namespace,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/ssl-redirect":      "false",
					"nginx.ingress.kubernetes.io/temporal-redirect": "https://www.aliyun.com",
				},
			},
			Spec: v1beta1.IngressSpec{
				Rules: []v1beta1.IngressRule{
					{
						Host: "*.bar.com",
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
								},
							},
						},
					},
				},
			},
		})

		time.Sleep(waitForLuaSync)
		for index := 1; index <= 5; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				Expect()
			resp.Status(http.StatusFound)
			time.Sleep(time.Second * 1)
		}

		ginkgo.By("Update ingress without 302 temporal redirect")
		f.UpdateIngress(&v1beta1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      host,
				Namespace: f.Namespace,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/ssl-redirect": "false",
				},
			},
			Spec: v1beta1.IngressSpec{
				Rules: []v1beta1.IngressRule{
					{
						Host: "*.bar.com",
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
								},
							},
						},
					},
				},
			},
		})

		time.Sleep(waitForLuaSync)
		for index := 1; index <= 5; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				Expect()
			resp.Status(http.StatusOK)
			resp.Body().Contains("old")
			time.Sleep(time.Second * 1)
		}
	})

	ginkgo.It("Support rewrite annotation", func() {
		host := "rewrite.bar.com"

		ginkgo.By("Create a ingress with rewriting app-root")
		f.EnsureIngress(&v1beta1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      host,
				Namespace: f.Namespace,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/app-root": "/nginx",
				},
			},
			Spec: v1beta1.IngressSpec{
				Rules: []v1beta1.IngressRule{
					{
						Host: "*.bar.com",
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
								},
							},
						},
					},
				},
			},
		})

		time.Sleep(waitForLuaSync)
		for index := 1; index <= 5; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				Expect()
			resp.Status(http.StatusFound)
			time.Sleep(time.Second * 1)
		}

		ginkgo.By("Create a ingress with rewriting force-ssl-redirect")
		f.UpdateIngress(&v1beta1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      host,
				Namespace: f.Namespace,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/force-ssl-redirect": "true",
				},
			},
			Spec: v1beta1.IngressSpec{
				Rules: []v1beta1.IngressRule{
					{
						Host: "*.bar.com",
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
								},
							},
						},
					},
				},
			},
		})

		time.Sleep(waitForLuaSync)
		for index := 1; index <= 5; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				Expect()
			resp.Status(http.StatusPermanentRedirect)
			time.Sleep(time.Second * 1)
		}

		ginkgo.By("Create a ingress with rewriting ssl-redirect")
		f.UpdateIngress(&v1beta1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      host,
				Namespace: f.Namespace,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/ssl-redirect": "true",
				},
			},
			Spec: v1beta1.IngressSpec{
				Rules: []v1beta1.IngressRule{
					{
						Host: "*.bar.com",
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
								},
							},
						},
					},
				},
			},
		})

		time.Sleep(waitForLuaSync)
		for index := 1; index <= 5; index++ {
			resp := f.HTTPTestClient().
				GET("/").
				WithHeader("Host", host).
				Expect()
			resp.Status(http.StatusPermanentRedirect)
			time.Sleep(time.Second * 1)
		}

		ginkgo.By("Create a ingress with rewriting rewrite-target")
		f.UpdateIngress(&v1beta1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      host,
				Namespace: f.Namespace,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/rewrite-target": "/",
					"nginx.ingress.kubernetes.io/ssl-redirect":   "false",
				},
			},
			Spec: v1beta1.IngressSpec{
				Rules: []v1beta1.IngressRule{
					{
						Host: "*.bar.com",
						IngressRuleValue: v1beta1.IngressRuleValue{
							HTTP: &v1beta1.HTTPIngressRuleValue{
								Paths: []v1beta1.HTTPIngressPath{
									{
										Path: "/nginx",
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
		for index := 1; index <= 5; index++ {
			resp := f.HTTPTestClient().
				GET("/nginx").
				WithHeader("Host", host).
				Expect()
			resp.Status(http.StatusOK)
			resp.Body().Contains("old")
			time.Sleep(time.Second * 1)
		}

		ginkgo.By("Create a ingress with rewriting rewrite-target and captured groups")
		f.UpdateIngress(&v1beta1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      host,
				Namespace: f.Namespace,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/rewrite-target": "/$1",
					"nginx.ingress.kubernetes.io/ssl-redirect":   "false",
				},
			},
			Spec: v1beta1.IngressSpec{
				Rules: []v1beta1.IngressRule{
					{
						Host: "*.bar.com",
						IngressRuleValue: v1beta1.IngressRuleValue{
							HTTP: &v1beta1.HTTPIngressRuleValue{
								Paths: []v1beta1.HTTPIngressPath{
									{
										Path: "/nginx/?(.*)",
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
		for index := 1; index <= 5; index++ {
			resp := f.HTTPTestClient().
				GET("/nginx").
				WithHeader("Host", host).
				Expect()
			resp.Status(http.StatusOK)
			resp.Body().Contains("old")
			time.Sleep(time.Second * 1)
		}
	})
})
