package main

import (
	"github.com/WingkaiHo/kube-ingress-external-dns/ingress"
)

func main() {
	helper := ingress.NewIngressHostHelper()
	helper.LoadIngress()
	helper.UpdateIngressController()
	helper.OuputHostFile()
	helper.WatchIngressChange()
}
