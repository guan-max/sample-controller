package main

import (
	"flag"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	clientset "k8s.io/sample-controller/pkg/generated/clientset/versioned"
	informers "k8s.io/sample-controller/pkg/generated/informers/externalversions"
	"k8s.io/sample-controller/pkg/signals"
	"time"
)

var (
	masterURL  string
	kubeconfig string
)

func main() {
	klog.InitFlags(nil)
	flag.Parse()

	ctx := signals.SetupSignalHandler()
	klog.FromContext(ctx)

	cfg, err := clientcmd.BuildConfigFromFlags(masterURL, kubeconfig)
	if err != nil {
		klog.Error(err, "Error building kubeconfig")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}
	//	k8s客户端
	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Error(err, "Error building kubernetes clientset")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}
	// 自定义客户端
	demoClient, err := clientset.NewForConfig(cfg)
	if err != nil {
		klog.Error(err, "Error building kubernetes demoClient")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}
	// k8s和自定义资源的informer 工厂
	kubeInformerFactor := kubeinformers.NewSharedInformerFactory(kubeClient, time.Second*30)
	demoInformerFactor := informers.NewSharedInformerFactory(demoClient, time.Second*30)

	// 初始化控制器，监听 Deployment 以及 Demo 资源变化
	controller := NewController(ctx, kubeClient, demoClient,
		kubeInformerFactor.Core().V1().Pods(),
		demoInformerFactor.Samplecontroller().V1alpha1().Demos())

	kubeInformerFactor.Start(ctx.Done())
	demoInformerFactor.Start(ctx.Done())

	if err = controller.Run(ctx, 2); err != nil {
		klog.Error(err, "Error running controller")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}
}

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}
