package kubernetes

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

type KubernetesConnManager struct {
	clientset   *kubernetes.Clientset
	namespace   string
	dialOptions []grpc.DialOption

	rpcTargets map[string]string
	selfTarget string

	mu      sync.RWMutex
	connMap map[string][]*grpc.ClientConn
}

// NewKubernetesConnManager 创建基于 Kubernetes 服务的连接管理器，用于服务发现。
func NewKubernetesConnManager(namespace string, options ...grpc.DialOption) (*KubernetesConnManager, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to create in-cluster config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %v", err)
	}

	k := &KubernetesConnManager{
		clientset:   clientset,
		namespace:   namespace,
		dialOptions: options,
		connMap:     make(map[string][]*grpc.ClientConn),
	}

	go k.watchEndpoints()

	return k, nil
}

func (k *KubernetesConnManager) initializeConns(serviceName string) error {
	port, err := k.getServicePort(serviceName)
	if err != nil {
		return err
	}

	endpoints, err := k.clientset.CoreV1().Endpoints(k.namespace).Get(context.Background(), serviceName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get endpoints for service %s: %v", serviceName, err)
	}

	// fmt.Println("Endpoints:", endpoints, "endpoints.Subsets:", endpoints.Subsets)

	var conns []*grpc.ClientConn
	for _, subset := range endpoints.Subsets {
		for _, address := range subset.Addresses {
			target := fmt.Sprintf("%s:%d", address.IP, port)
			// fmt.Println("IP target:", target)
			conn, err := grpc.Dial(target, append(k.dialOptions, grpc.WithTransportCredentials(insecure.NewCredentials()))...)
			if err != nil {
				return fmt.Errorf("failed to dial endpoint %s: %v", target, err)
			}
			conns = append(conns, conn)
		}
	}

	k.mu.Lock()
	k.connMap[serviceName] = conns
	k.mu.Unlock()

	return nil
}

// GetConns 返回指定 Kubernetes 服务名的所有 gRPC 客户端连接。
func (k *KubernetesConnManager) GetConns(ctx context.Context, serviceName string, opts ...grpc.DialOption) ([]*grpc.ClientConn, error) {
	k.mu.RLock()

	conns, exists := k.connMap[serviceName]
	k.mu.RUnlock()
	if exists {
		return conns, nil
	}

	k.mu.Lock()
	// 释放读锁后，检查是否有其他协程已完成连接初始化
	conns, exists = k.connMap[serviceName]
	if exists {
		return conns, nil
	}
	k.mu.Unlock()

	if err := k.initializeConns(serviceName); err != nil {
		fmt.Println("Failed to initialize connections:", err)
		return nil, fmt.Errorf("failed to initialize connections for service %s: %v", serviceName, err)
	}

	return k.connMap[serviceName], nil
}

// GetConn 返回指定 Kubernetes 服务名的单个 gRPC 客户端连接。
func (k *KubernetesConnManager) GetConn(ctx context.Context, serviceName string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	var target string

	if k.rpcTargets[serviceName] == "" {
		var err error

		svcPort, err := k.getServicePort(serviceName)
		if err != nil {
			return nil, err
		}

		target = fmt.Sprintf("%s.%s.svc.cluster.local:%d", serviceName, k.namespace, svcPort)

		// fmt.Println("SVC target:", target)
	} else {
		target = k.rpcTargets[serviceName]
	}

	return grpc.DialContext(
		ctx,
		target,
		append([]grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(1024*1024*10), grpc.MaxCallSendMsgSize(1024*1024*20)),
		}, k.dialOptions...)...,
	)
}

// GetSelfConnTarget 返回当前服务的连接目标。
func (k *KubernetesConnManager) GetSelfConnTarget() string {
	if k.selfTarget == "" {
		hostName := os.Getenv("HOSTNAME")

		pod, err := k.clientset.CoreV1().Pods(k.namespace).Get(context.Background(), hostName, metav1.GetOptions{})
		if err != nil {
			log.Printf("failed to get pod %s: %v \n", hostName, err)
		}

		for pod.Status.PodIP == "" {
			pod, err = k.clientset.CoreV1().Pods(k.namespace).Get(context.TODO(), hostName, metav1.GetOptions{})
			if err != nil {
				log.Printf("Error getting pod: %v \n", err)
			}

			time.Sleep(3 * time.Second)
		}

		var selfPort int32

		for _, port := range pod.Spec.Containers[0].Ports {
			if port.ContainerPort != 10001 {
				selfPort = port.ContainerPort
				break
			}
		}

		k.selfTarget = fmt.Sprintf("%s:%d", pod.Status.PodIP, selfPort)
	}

	return k.selfTarget
}

// AddOption 在现有选项后追加 gRPC dial 选项。
func (k *KubernetesConnManager) AddOption(opts ...grpc.DialOption) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.dialOptions = append(k.dialOptions, opts...)
}

// CloseConn 关闭指定的 gRPC 客户端连接。
func (k *KubernetesConnManager) CloseConn(conn *grpc.ClientConn) {
	conn.Close()
}

// Close 关闭 KubernetesConnManager 管理的所有 gRPC 连接。
func (k *KubernetesConnManager) Close() {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, conns := range k.connMap {
		for _, conn := range conns {
			_ = conn.Close()
		}
	}
	k.connMap = make(map[string][]*grpc.ClientConn)
}

func (k *KubernetesConnManager) Register(serviceName, host string, port int, opts ...grpc.DialOption) error {
	return nil
}

func (k *KubernetesConnManager) UnRegister() error {
	return nil
}

func (k *KubernetesConnManager) GetUserIdHashGatewayHost(ctx context.Context, userId string) (string, error) {
	return "", nil
}

func (k *KubernetesConnManager) getServicePort(serviceName string) (int32, error) {
	var svcPort int32

	svc, err := k.clientset.CoreV1().Services(k.namespace).Get(context.Background(), serviceName, metav1.GetOptions{})
	if err != nil {
		fmt.Print("namespace:", k.namespace)
		return 0, fmt.Errorf("failed to get service %s: %v", serviceName, err)
	}

	if len(svc.Spec.Ports) == 0 {
		return 0, fmt.Errorf("service %s has no ports defined", serviceName)
	}

	for _, port := range svc.Spec.Ports {
		// fmt.Println(serviceName, " Now Get Port:", port.Port)
		if port.Port != 10001 {
			svcPort = port.Port
			break
		}
	}

	return svcPort, nil
}

// watchEndpoints 监听 Pod 资源变化。
func (k *KubernetesConnManager) watchEndpoints() {
	informerFactory := informers.NewSharedInformerFactory(k.clientset, time.Minute*10)
	informer := informerFactory.Core().V1().Pods().Informer()

	// 监听 Pod 变化（新增、更新、删除）
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			k.handleEndpointChange(obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			k.handleEndpointChange(newObj)
		},
		DeleteFunc: func(obj interface{}) {
			k.handleEndpointChange(obj)
		},
	})

	informerFactory.Start(context.Background().Done())
	<-context.Background().Done() // 永久阻塞
}

func (k *KubernetesConnManager) handleEndpointChange(obj interface{}) {
	endpoint, ok := obj.(*v1.Endpoints)
	if !ok {
		return
	}
	serviceName := endpoint.Name
	if err := k.initializeConns(serviceName); err != nil {
		fmt.Printf("Error initializing connections for %s: %v\n", serviceName, err)
	}
}
