package lua

import (
	"strings"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"net/http"
	"io/ioutil"
	appsv1beta1 "k8s.io/api/apps/v1beta1"
	"k8s.io/ingress-nginx/test/e2e/framework"
	"k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"time"
	"github.com/parnurzeal/gorequest"
)

const (
	oldNginxName  = "old-nginx"
	oldNginxImage = "registry.cn-hangzhou.aliyuncs.com/acs-sample/old-nginx"
	nginxPort     = 80
)

var _ = framework.IngressNginxDescribe("Dynamic Server", func() {
	f := framework.NewDefaultFramework("dynamic-server")
	host := "dus.foo.com"

	BeforeEach(func() {
		err := framework.UpdateDeployment(f.KubeClientSet, f.IngressController.Namespace, "nginx-ingress-controller", 1,
			func(deployment *appsv1beta1.Deployment) error {
				args := deployment.Spec.Template.Spec.Containers[0].Args
				args = append(args, "--enable-dynamic-certificates=true")
				args = append(args, "--enable-ssl-chain-completion=false")
				args = append(args, "--enable-dynamic-servers=true")
				deployment.Spec.Template.Spec.Containers[0].Args = args
				_, err := f.KubeClientSet.AppsV1beta1().Deployments(f.IngressController.Namespace).Update(deployment)
				return err
			})
		Expect(err).NotTo(HaveOccurred())

		f.WaitForNginxConfiguration(
			func(cfg string) bool {
				return strings.Contains(cfg, "ok, res = pcall(require, \"certificate\")") &&
					strings.Contains(cfg, "balancer.access()")
			})

		f.NewDeployment(oldNginxName, oldNginxImage, nginxPort, 1)
	})

	Context("when updating an ingress", func() {
		It("create a new ingress without reloading", func() {
			var nginxConfig string
			f.WaitForNginxConfiguration(func(cfg string) bool {
				nginxConfig = cfg
				return true
			})

			// should return 404
			for index := 1; index <= 5; index++ {
				resp, _, errs := gorequest.New().
					SetDebug(true).
					SetCurlCommand(true).
					Get(f.IngressController.HTTPURL).
					Set("Host", host).
					End()

				Expect(len(errs)).Should(BeNumerically("==", 0))
				Expect(resp.StatusCode).Should(Equal(http.StatusNotFound))
				time.Sleep(time.Second * 1)
			}

			// create ingress
			f.EnsureIngress(&v1beta1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:        host,
					Namespace:   f.IngressController.Namespace,
					Annotations: map[string]string{},
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
			Expect(nginxConfig).Should(Equal(newNginxConfig))

			// should return 200
			for index := 1; index <= 5; index++ {
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
})
