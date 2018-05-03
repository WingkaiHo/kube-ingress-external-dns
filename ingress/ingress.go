package ingress

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"sync"

	"github.com/golang/glog"

	corev1 "k8s.io/api/core/v1"
	v1beta1 "k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

var kubeConfig = flag.String("kubeconfig", "./config", "Path to a kube config. Only required if out-of-cluster.")
var hosfilePath = flag.String("hosfilePath", "/etc/dnsmasq.d/ingress.host", "The hostfile path of dnsmsq to load hostfile")
var isInCluster = flag.Bool("run_in_k8s_cluster", false, "The app run in k8s cluster")

type IngressHostHelper struct {
	k8sClient   *kubernetes.Clientset
	ingressMap  map[types.UID]v1beta1.Ingress
	hosfilePath string
	controllers map[string]corev1.Pod
	m           *sync.Mutex
}

func init() {
	flag.Parse()
}

func NewIngressHostHelper() (helper *IngressHostHelper) {
	var config *restclient.Config
	var err error

	helper = new(IngressHostHelper)
	helper.ingressMap = make(map[types.UID]v1beta1.Ingress, 10)
	helper.controllers = make(map[string]corev1.Pod, 3)
	helper.hosfilePath = *hosfilePath
	helper.m = new(sync.Mutex)

	if *isInCluster == false {
		glog.Infoln(*kubeConfig)
		config, err = clientcmd.BuildConfigFromFlags("", *kubeConfig)
		if err != nil {
			panic(err)
		}
	} else {
		config, err = restclient.InClusterConfig()
		glog.Infoln("Using the inClusterConfig.  This might not work")
		if err != nil {
			panic(err.Error())
		}
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

func (helper *IngressHostHelper) WatchIngressControllerUpdate() (chan<- struct{}, error) {
	opts := metav1.ListOptions{LabelSelector: "app=ingress-nginx"}
	eventInterface, err := helper.k8sClient.CoreV1().Pods(metav1.NamespaceAll).Watch(opts)
	stop := make(chan struct{})
	if err != nil {
		return stop, err
	}

	go func() {
		for {
			select {
			case <-stop:
				return
			case event := <-eventInterface.ResultChan():
				if event.Type == watch.Added || event.Type == watch.Deleted {
					obj := event.Object.DeepCopyObject()
					pod, ok := obj.(*corev1.Pod)
					update := false
					if ok {
						helper.m.Lock()
						if event.Type == watch.Deleted {
							glog.Info("Delete nginx controller: ", pod.Status.HostIP)
							delete(helper.controllers, pod.Status.HostIP)
							update = true
						} else {
							if _, persent := helper.controllers[pod.Status.HostIP]; persent == false {
								helper.controllers[pod.Status.HostIP] = *pod
								glog.Info("Add nginx controller: ", pod.Status.HostIP)
								update = true
							}
						}
						helper.m.Unlock()

						if update {
							helper.OuputHostFile()
						}
					}
				}
			}
		}
	}()

	return stop, nil
}

func (helper *IngressHostHelper) UpdateIngressController() {
	pods, err := helper.k8sClient.CoreV1().Pods(metav1.NamespaceAll).List(
		metav1.ListOptions{LabelSelector: "app=ingress-nginx"})

	if err != nil {
		fmt.Println(err)
		return
	}

	for i := range pods.Items {
		helper.controllers[pods.Items[i].Status.HostIP] = pods.Items[i]
		glog.Infoln("Add nginx controller:", helper.controllers[pods.Items[i].Status.HostIP].Status.HostIP)
	}

}

func (helper *IngressHostHelper) OutputHostItem(buf *bytes.Buffer, ip, domain string) {
	s := fmt.Sprintf("%s \t %s \n", ip, domain)
	buf.WriteString(s)
}

func (helper *IngressHostHelper) OuputHostFile() {
	var buf bytes.Buffer

	// lock
	helper.m.Lock()
	defer helper.m.Unlock()

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

	glog.Info("Output to hostfile: \n", buf.String())
}

func (helper *IngressHostHelper) WatchIngressChange() chan<- struct{} {
	// /home/heyj/workspace/git/ingress/internal/ingress/controller
	ingEventHandler := cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			ing, ok := obj.(*v1beta1.Ingress)
			if ok {
				if _, present := helper.ingressMap[ing.UID]; !present {
					helper.m.Lock()
					glog.Info("Add ingress item:", *ing)
					helper.ingressMap[ing.UID] = *ing
					helper.m.Unlock()
					// Update hostfile
					helper.OuputHostFile()
				}
			}
		},
		DeleteFunc: func(obj interface{}) {
			ing, ok := obj.(*v1beta1.Ingress)
			if ok {
				glog.Info("Delete ingress item:", *ing)

				helper.m.Lock()
				delete(helper.ingressMap, ing.UID)
				helper.m.Unlock()

				// update hostfile
				helper.OuputHostFile()
			}

		},
		UpdateFunc: func(old, cur interface{}) {
			oldIng, ok1 := old.(*v1beta1.Ingress)
			curlIng, ok2 := cur.(*v1beta1.Ingress)
			if ok1 && ok2 {
				glog.Info("Update ingress item:", *oldIng)

				helper.m.Lock()
				helper.ingressMap[oldIng.UID] = *curlIng
				helper.m.Unlock()

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
	go controller.Run(stop)
	return stop
}
