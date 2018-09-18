package ingress

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"reflect"
	"sync"
	//"time"

	"github.com/golang/glog"

	"github.com/WingkaiHo/kube-ingress-external-dns/safemap"
	corev1 "k8s.io/api/core/v1"
	v1beta1 "k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	//	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

var kubeConfig = flag.String("kubeconfig", "./config", "Path to a kube config. Only required if out-of-cluster.")
var hosfilePath = flag.String("hosfilePath", "/etc/dnsmasq.d/ingress.host", "The hostfile path of dnsmsq to load hostfile")
var isInCluster = flag.Bool("run_in_k8s_cluster", false, "The app run in k8s cluster")
var ingressPodLable = flag.String("pod_lable", "k8s-app=ingress-nginx", "The pod lable of ingress controller")

type IngressHostHelper struct {
	k8sClient   *kubernetes.Clientset
	ingressMap  safemap.SafeMap
	hosfilePath string
	controllers safemap.SafeMap
	m           *sync.Mutex
}

func init() {
	flag.Parse()
}

func NewIngressHostHelper() (helper *IngressHostHelper) {
	var config *restclient.Config
	var err error

	helper = new(IngressHostHelper)
	helper.ingressMap = safemap.New()
	helper.controllers = safemap.New()
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
		//glog.
		return
	}

	for i := range ingresses.Items {
		glog.Infoln("Load ingress item:", ingresses.Items[i].Name)
		id := ingresses.Items[i].UID
		helper.ingressMap.Insert(string(id), ingresses.Items[i])
	}
}

func getIngressControllerPodEventInterface(helper *IngressHostHelper, label string) (watch.Interface, error) {
	opts := metav1.ListOptions{LabelSelector: label}
	eventInterface, err := helper.k8sClient.CoreV1().Pods(metav1.NamespaceAll).Watch(opts)
	if err != nil {
		glog.Info("event interface err: ", err.Error())
		return nil, err
	}
	glog.Info("Connect k8s master successful.")
	return eventInterface, nil
}

func processControllerPodEvent(helper *IngressHostHelper, event watch.Event) {
	if event.Type == watch.Added || event.Type == watch.Deleted {
		obj := event.Object.DeepCopyObject()
		pod, ok := obj.(*corev1.Pod)
		update := false
		if ok {
			if event.Type == watch.Deleted {
				glog.Info("Delete nginx controller: ", pod.Status.HostIP)
				helper.controllers.Delete(pod.Status.HostIP)
				update = true
			} else {
				if _, persent := helper.controllers.Find(pod.Status.HostIP); persent == false {
					helper.controllers.Insert(pod.Status.HostIP, *pod)
					glog.Info("Add nginx controller: ", pod.Status.HostIP)
					update = true
				}
			}

			if update {
				helper.OuputHostFile()
			}
		}
	}

}

func (helper *IngressHostHelper) WatchIngressControllerUpdate() (chan<- struct{}, error) {
	glog.Info("Start watch ingress controller ")
	eventInterface, err := getIngressControllerPodEventInterface(helper, *ingressPodLable)
	stop := make(chan struct{})
	if err != nil {
		glog.Info("event interface err: ", err.Error())
		return stop, err
	}

	go func() {
		for {
			select {
			case <-stop:
				return
			case event, normal := <-eventInterface.ResultChan():
				if normal == false {
					glog.Info("event interface close:")
					eventInterface, err = getIngressControllerPodEventInterface(helper, *ingressPodLable)
					if err != nil {
						panic(err)
					}
				} else {
					processControllerPodEvent(helper, event)
				}

			}
		}
	}()

	return stop, nil
}

func (helper *IngressHostHelper) UpdateIngressController() {
	pods, err := helper.k8sClient.CoreV1().Pods(metav1.NamespaceAll).List(
		metav1.ListOptions{LabelSelector: "k8s-app=ingress-nginx"})

	if err != nil {
		fmt.Println(err)
		return
	}

	for i := range pods.Items {
		//helper.controllers[pods.Items[i].Status.HostIP] = pods.Items[i]
		helper.controllers.Insert(pods.Items[i].Status.HostIP, pods.Items[i])
		glog.Infoln("Add nginx controller:", pods.Items[i].Status.HostIP)
	}

}

func (helper *IngressHostHelper) OutputHostItem(buf *bytes.Buffer, ip, domain string) {
	s := fmt.Sprintf("%s \t %s \n", ip, domain)
	buf.WriteString(s)
}

func MakeDumpIngressHostFunc(helper *IngressHostHelper) func(map[string]interface{}) []interface{} {
	return func(store map[string]interface{}) []interface{} {
		output := make([]interface{}, 0)
		for _, value := range store {
			ingress := value.(v1beta1.Ingress)
			for i := range ingress.Spec.Rules {
				output = append(output, ingress.Spec.Rules[i].Host)
			}
		}
		return output
	}
}

func MakeDumpIngressIpFunc(helper *IngressHostHelper) func(map[string]interface{}) []interface{} {
	return func(store map[string]interface{}) []interface{} {
		output := make([]interface{}, 0)
		for _, value := range store {
			pod := value.(corev1.Pod)
			output = append(output, pod.Status.HostIP)
		}
		return output
	}
}

func (helper *IngressHostHelper) OuputHostFile() {
	var buf bytes.Buffer

	vhosts := helper.ingressMap.Dump(MakeDumpIngressHostFunc(helper))
	ips := helper.controllers.Dump(MakeDumpIngressIpFunc(helper))

	glog.Infoln("Ingress domains:", vhosts)
	glog.Infoln("Ingress nginx ips:", ips)
	for _, host := range vhosts {
		for _, ip := range ips {
			helper.OutputHostItem(&buf, ip.(string), host.(string))
		}
	}

	fmt.Println(buf.String())
	f, err := os.Create(helper.hosfilePath)
	if err != nil {
		glog.Errorln(err, f)
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
				if _, present := helper.ingressMap.Find(string(ing.UID)); !present {
					glog.Info("Add ingress item:", *ing)
					helper.ingressMap.Insert(string(ing.UID), *ing)
					helper.OuputHostFile()
				}
			}
		},
		DeleteFunc: func(obj interface{}) {
			ing, ok := obj.(*v1beta1.Ingress)
			if ok {
				glog.Info("Delete ingress item:", *ing)
				helper.ingressMap.Delete(string(ing.UID))
				helper.OuputHostFile()
			}

		},
		UpdateFunc: func(old, cur interface{}) {
			oldIng, ok1 := old.(*v1beta1.Ingress)
			currIng, ok2 := cur.(*v1beta1.Ingress)
			if ok1 && ok2 && !reflect.DeepEqual(oldIng, currIng) {
				glog.Info("Update ingress item:", *oldIng)

				helper.ingressMap.Insert(string(oldIng.UID), *currIng)

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
		//time.Second*10,
		0,
		ingEventHandler)

	stop := make(chan struct{})
	go controller.Run(stop)
	return stop
}
