package ingress

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	//	"time"

	"github.com/golang/glog"

	corev1 "k8s.io/api/core/v1"
	v1beta1 "k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

var kubeConfig = flag.String("kubeconfig", "./config", "Path to a kube config. Only required if out-of-cluster.")
var hosfilePath = flag.String("hosfilePath", "/etc/dnsmsq.d/ingress.host", "The hostfile path of dnsmsq to load hostfile")
var isInCluster = flag.Bool("run_in_k8s_cluster", false, "The app run in k8s cluster")

type IngressHostHelper struct {
	k8sClient   *kubernetes.Clientset
	ingressMap  map[types.UID]v1beta1.Ingress
	hosfilePath string
	controllers map[types.UID]corev1.Pod
}

func init() {
	flag.Parse()
}

func NewIngressHostHelper() (helper *IngressHostHelper) {
	helper = new(IngressHostHelper)
	helper.ingressMap = make(map[types.UID]v1beta1.Ingress, 10)
	helper.controllers = make(map[types.UID]corev1.Pod, 3)
	helper.hosfilePath = *hosfilePath

	fmt.Println(*kubeConfig)
	config, err := clientcmd.BuildConfigFromFlags("", *kubeConfig)
	if err != nil {
		panic(err)
	}

	helper.k8sClient, err = kubernetes.NewForConfig(config)
	if err != nil {
		panic(err)
	}

	return helper
}

func (helper *IngressHostHelper) LoadIngress() {
	ingresses, err := helper.k8sClient.ExtensionsV1beta1().Ingresses(metav1.NamespaceAll).List(metav1.ListOptions{})
	if err != nil {
		fmt.Println(err)
		return
	}

	for i := range ingresses.Items {
		glog.Infoln("Load ingress item:", ingresses.Items[i].Name)
		id := ingresses.Items[i].UID
		helper.ingressMap[id] = ingresses.Items[i]
	}
}

func (helper *IngressHostHelper) UpdateIngressController() {
	pods, err := helper.k8sClient.CoreV1().Pods(metav1.NamespaceAll).List(
		metav1.ListOptions{LabelSelector: "app=ingress-nginx"})

	if err != nil {
		fmt.Println(err)
		return
	}

	for i := range pods.Items {
		helper.controllers[pods.Items[i].UID] = pods.Items[i]
		glog.Infoln("Add ingress controller item:", helper.controllers[pods.Items[i].UID].Status.HostIP)
	}

}

func (helper *IngressHostHelper) OutputHostItem(buf *bytes.Buffer, ip, domain string) {
	s := fmt.Sprintf("%s \t %s \n", ip, domain)
	buf.WriteString(s)
}

func (helper *IngressHostHelper) OuputHostFile() {
	var buf bytes.Buffer
	for _, ingress := range helper.ingressMap {
		// if exists loadbalance ip user load balance ip
		for _, pod := range helper.controllers {
			for j := range ingress.Spec.Rules {
				helper.OutputHostItem(&buf, pod.Status.HostIP, ingress.Spec.Rules[j].Host)
			}
		}
	}

	f, err := os.Create(helper.hosfilePath)
	if err != nil {
		return
	}

	defer f.Close()
	// clear old host item
	f.Truncate(0)
	// write new host item
	f.Write(buf.Bytes())

	fmt.Println(buf.String())
}

func (helper *IngressHostHelper) WatchIngressChange() {
	// /home/heyj/workspace/git/ingress/internal/ingress/controller
	ingEventHandler := cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			ing, ok := obj.(*v1beta1.Ingress)
			if ok {
				fmt.Println("Add item:", *ing)
				helper.ingressMap[ing.UID] = *ing
				// update hostfile
				helper.OuputHostFile()
			}
		},
		DeleteFunc: func(obj interface{}) {
			ing, ok := obj.(*v1beta1.Ingress)
			if ok {
				fmt.Println("Delete item:", *ing)
				delete(helper.ingressMap, ing.UID)

				// update hostfile
				helper.OuputHostFile()
			}

		},
		UpdateFunc: func(old, cur interface{}) {
			oldIng, ok1 := old.(*v1beta1.Ingress)
			curlIng, ok2 := cur.(*v1beta1.Ingress)
			if ok1 && ok2 {
				fmt.Println("Update item:", *oldIng)
				helper.ingressMap[oldIng.UID] = *curlIng
				// update hostfile
				helper.OuputHostFile()
			}
		},
	}

	watchlist := cache.NewListWatchFromClient(helper.k8sClient.ExtensionsV1beta1().RESTClient(),
		"ingresses", metav1.NamespaceAll, fields.Everything())

	_, controller := cache.NewInformer(
		watchlist,
		&v1beta1.Ingress{},
		//time.Second*100,
		0,
		ingEventHandler)

	stop := make(chan struct{})
	controller.Run(stop)
	//go controller.Run(stop)
	//for {
	//	time.Sleep(time.Second)
	//}

}
