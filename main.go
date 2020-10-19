package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/WingkaiHo/kube-ingress-external-dns/ingress"
	"github.com/golang/glog"
)

func main() {
	helper := ingress.NewIngressHostHelper()
	errLoadIgress := helper.LoadIngress()
	errUpdateController := helper.UpdateIngressController()

	// 解决启动时候 get pod 失败的导出空host表导致dns失败
	if errLoadIgress == nil && errUpdateController == nil {
		helper.OuputHostFile()
	}

	stopWatchIngress := helper.WatchIngressChange()
	stopWatchController := helper.WatchIngressControllerChange()

	signalChan := make(chan os.Signal)
	signal.Notify(signalChan, os.Interrupt, os.Kill, syscall.SIGUSR1, syscall.SIGUSR2)
	s := <-signalChan
	glog.Infoln("Exit by single: ", s)
	// 结束Ingress/nginx controller 监控
	stopWatchIngress <- struct{}{}
	stopWatchController <- struct{}{}
}
