## kube-ingress-external-dns
- kube-ingress-external-dns 监控k8s集群ingress host 变化，自动生成/更新 ingress.host文件。此host文件和dnsmasq-nanny配置使用实现ingress host可以在k8s集群进行dns解析， 便于内部测试服务等，可以通过ingress host域名访问服务。
- 同时可以做为公司内部网网络在集群解析， 让集群服务可以通过内部域名访问集群外服务


### kubernates 上部署
- dns相关配置
```
apiVersion: v1
kind: ConfigMap
metadata:
  name: ingress-host-dns
  namespace: kube-system
data:
#配置此dns上游dns
  resolv.dnsmasq.conf: |
    nameserver 114.114.114.114
#下面可以添加公司内部系统域名
  private.dnsmasq.hosts: |
#192.168.0.1 you.app.domain
```

- ingress host dns 配置
 和dnsmasq-nanny运行在同一pod上， 当更新host文件以后， nanny自动重新启动dnsmasq， 由于dnsmasq后去hostdir更新事件，但是删除了host item不会清理缓冲，需要通过nanny重新启动他
```
apiVersion: extensions/v1beta1
kind: Deployment
metadata:
  name: ingress-host-dns
  namespace: kube-system
  labels:
    k8s-app: heapster
    kubernetes.io/cluster-service: "true"
    addonmanager.kubernetes.io/mode: Reconcile
spec:
  replicas: 1
  selector:
    matchLabels:
      k8s-app: ingress-host-dns
      version: v1.0
  template:
    metadata:
      labels:
        k8s-app: ingress-host-dns
        version: v1.0
      annotations:
        scheduler.alpha.kubernetes.io/critical-pod: ''
    spec:
      containers:
        - image: kube-ingress-host-dns:v1.0
          name: kube-ingress-dns
          command:
            - kube-ingress-external-dns
          args:
            - -alsologtostderr=true
            - -hosfilePath=/etc/dnsmasq.host/ingress.host
            - -run_in_k8s_cluster=true
          volumeMounts:
          - name: hostsdir
            mountPath: /etc/dnsmasq.host/ 
          resources:
            requests:
              cpu: 150m
              memory: 20Mi
            limits:
              cpu: 500m
              memory: 100Mi 
        - name: dnsmasq
          image: <you-registry:port>/gcr.io/google_containers/k8s-dns-dnsmasq-nanny-amd64:1.14.2
          imagePullPolicy: IfNotPresent
          volumeMounts:
          - name: hostsdir
            mountPath: /etc/dnsmasq.host/
          - name: resolv-conf
            mountPath: /etc/dnsmasq.host/resolv.dnsmasq.conf
            subPath: resolv.dnsmasq.conf 
          - name: hosts-conf
            mountPath: /etc/dnsmasq.host/private.dnsmasq.hosts
            subPath: private.dnsmasq.hosts
          resources:
            requests:
              cpu: 100m
              memory: 70Mi
            limits:
              cpu: 500m
              memory: 170Mi 
          args:
          - -v=2
          - -logtostderr
          - -configDir=/etc/dnsmasq.host/
          - -restartDnsmasq=true
          - --
          - -k
          - --cache-size=1000
          - --log-facility=-
          - --user=root
          - --no-hosts
          - --strict-order
          - --resolv-file=/etc/dnsmasq.host/resolv.dnsmasq.conf
          - --hostsdir=/etc/dnsmasq.host/
          ports:
          - containerPort: 53
            name: dns
            protocol: UDP
          - containerPort: 53
            name: dns-tcp
            protocol: TCP
      volumes:
      - name: hostsdir
        emptyDir: {}
      - name: resolv-conf 
        configMap:
          name: ingress-host-dns
          items:
          - key: resolv.dnsmasq.conf
            path: resolv.dnsmasq.conf
      - name: hosts-conf
        configMap:
          name: ingress-host-dns
          items:
          - key: private.dnsmasq.hosts
            path: private.dnsmasq.hosts
      serviceAccountName: ingress-host-dns
```
- 服务配置
导出服务53tcp和udp端口
```
apiVersion: v1
kind: Service
metadata:
  name: ingress-host-dns
  namespace: kube-system
  labels:
    k8s-app: ingress-host-dns
spec:
  selector:
    k8s-app: ingress-host-dns
  clusterIP:  10.233.0.4// 修改为k8s集群ip段的ip， 由于dns不能使用动态集群ip
  ports:
  - name: dns
    port: 53
    protocol: UDP
  - name: dns-tcp
    port: 53
```

- 关联到k8s-dns上游dns
  把k8s上游dns配置为此dns ip
```
apiVersion: v1
kind: ConfigMap
metadata:
  name: kube-dns
  namespace: kube-system
data:
  upstreamNameservers: |
    ["10.233.0.4"]

```


