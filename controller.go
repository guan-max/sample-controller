/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/time/rate"

	//appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	//appsinformers "k8s.io/client-go/informers/apps/v1"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	//appslisters "k8s.io/client-go/listers/apps/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	samplev1alpha1 "k8s.io/sample-controller/pkg/apis/democontroller/v1alpha1"
	clientset "k8s.io/sample-controller/pkg/generated/clientset/versioned"
	samplescheme "k8s.io/sample-controller/pkg/generated/clientset/versioned/scheme"
	informers "k8s.io/sample-controller/pkg/generated/informers/externalversions/democontroller/v1alpha1"
	listers "k8s.io/sample-controller/pkg/generated/listers/democontroller/v1alpha1"
)

const controllerAgentName = "demo-controller"

const (
	// SuccessSynced is used as part of the Event 'reason' when a Foo is synced
	SuccessSynced = "Synced"
	// ErrResourceExists is used as part of the Event 'reason' when a Foo fails
	// to sync due to a Deployment of the same name already existing.
	ErrResourceExists = "ErrResourceExists"

	// MessageResourceExists is the message used for Events when a resource
	// fails to sync due to a Deployment already existing
	MessageResourceExists = "Resource %q already exists and is not managed by Foo"
	// MessageResourceSynced is the message used for an Event fired when a Foo
	// is synced successfully
	MessageResourceSynced = "Foo synced successfully"
)

// Controller is the controller implementation for Foo resources
type Controller struct {
	// kubeclientset is a standard kubernetes clientset
	kubeclientset kubernetes.Interface
	// sampleclientset is a clientset for our own API group
	sampleclientset clientset.Interface

	// deploymentsLister is able to list/get Deployments from a shared informer's
	//deploymentsLister appslisters.DeploymentLister
	// 用于同步 Deployment 资源的 InformerSynced 接口。
	//deploymentsSynced cache.InformerSynced
	podsLister corelisters.PodLister
	podsSynced cache.InformerSynced
	// foosLister is able to list/get Foo resources from a shared informer's
	demosLister listers.DemoLister
	// 用于同步 Foo 资源的 InformerSynced 接口。
	demosSynced cache.InformerSynced

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens. This
	// means we can ensure we only process a fixed amount of resources at a
	// time, and makes it easy to ensure we are never processing the same item
	// simultaneously in two different workers.
	// 为了缓冲事件处理，这里使用队列暂存事件
	workqueue workqueue.TypedRateLimitingInterface[string]
	// recorder is an event recorder for recording Event resources to the
	// Kubernetes API.
	// 用于记录事件,将事件记录到kubernetes api中
	recorder record.EventRecorder
}

// NewController returns a new sample controller
func NewController(
	ctx context.Context,
	kubeclientset kubernetes.Interface,
	sampleclientset clientset.Interface,
	podInformer coreinformers.PodInformer,
	//deploymentInformer appsinformers.DeploymentInformer,
	demoInformer informers.DemoInformer) *Controller {
	logger := klog.FromContext(ctx)

	// Create event broadcaster
	// Add sample-controller types to the default Kubernetes Scheme so Events can be
	// logged for sample-controller types.
	// 添加 sample-controller 类型到默认的 kubernetes scheme 中，以便事件可以被记录到 kubernetes api 中。
	// utilruntime.Must() 是一个简化错误处理的实用函数，用于将可能出现的错误转换为恐慌（panic），以确保添加到 Scheme 中的操作成功。
	utilruntime.Must(samplescheme.AddToScheme(scheme.Scheme))
	logger.V(4).Info("Creating event broadcaster")

	eventBroadcaster := record.NewBroadcaster(record.WithContext(ctx))
	// 启动日志记录
	eventBroadcaster.StartStructuredLogging(0)
	// 启动事件记录
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerAgentName})
	ratelimiter := workqueue.NewTypedMaxOfRateLimiter(
		workqueue.NewTypedItemExponentialFailureRateLimiter[string](5*time.Millisecond, 1000*time.Second),
		&workqueue.TypedBucketRateLimiter[string]{Limiter: rate.NewLimiter(rate.Limit(50), 300)},
	)
	controller := &Controller{
		kubeclientset:   kubeclientset,
		sampleclientset: sampleclientset,
		podsLister:      podInformer.Lister(),
		podsSynced:      podInformer.Informer().HasSynced,
		demosLister:     demoInformer.Lister(),
		demosSynced:     demoInformer.Informer().HasSynced,
		workqueue:       workqueue.NewTypedRateLimitingQueue(ratelimiter),
		recorder:        recorder,
	}

	logger.Info("Setting up event handlers")
	// Set up an event handler for when Foo resources change
	// 设置对 Foo 资源变化的事件处理函数（Add、Update 均通过 enqueueFoo 处理）
	demoInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueueFoo,
		UpdateFunc: func(old, new interface{}) {
			controller.enqueueFoo(new)
		},
	})
	// Set up an event handler for when Deployment resources change. This
	// handler will lookup the owner of the given Deployment, and if it is
	// owned by a Foo resource then the handler will enqueue that Foo resource for
	// processing. This way, we don't need to implement custom logic for
	// handling Deployment resources. More info on this pattern:
	// https://github.com/kubernetes/community/blob/8cafef897a22026d42f5e5bb3f104febe7e29830/contributors/devel/controllers.md
	// 设置对 Deployment 资源变化的事件处理函数数Add、Update、Delete 均通过 handleObject 处理
	podInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.handleObject,
		UpdateFunc: func(old, new interface{}) {
			newDepl := new.(*corev1.Pod)
			oldDepl := old.(*corev1.Pod)
			// Deployment 的 RV 发生变化后，本应该同步 Foo 但实际很多情况有问题，比如拉取下游镜像时改动的 Foo 没执行。
			// 如果新旧资源的版本号相同，则表示这是一个周期性的重新同步事件，不需要执行进一步的处理
			if newDepl.ResourceVersion == oldDepl.ResourceVersion {
				// Periodic resync will send update events for all known Deployments.
				// Two different versions of the same Deployment will always have different RVs.
				return
			}
			controller.handleObject(new)
		},
		DeleteFunc: controller.handleObject,
	})

	return controller
}

// Run will set up the event handlers for types we are interested in, as well
// as syncing informer caches and starting workers. It will block until stopCh
// is closed, at which point it will shutdown the workqueue and wait for
// workers to finish processing their current work items.
// 等待 Informer 同步完成，并发 runWorker，处理队列内事件
func (c *Controller) Run(ctx context.Context, workers int) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()
	logger := klog.FromContext(ctx)

	// Start the informer factories to begin populating the informer caches
	logger.Info("Starting Demo controller")

	// Wait for the caches to be synced before starting workers
	logger.Info("Waiting for informer caches to sync")

	if ok := cache.WaitForCacheSync(ctx.Done(), c.podsSynced, c.demosSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	logger.Info("Starting workers", "count", workers)
	// Launch two workers to process Foo resources
	for i := 0; i < workers; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	logger.Info("Started workers")
	// ctx.Done() 返回一个通道（channel），当上下文结束时，这个通道会被关闭。
	//通过使用 <-ctx.Done()，我们可以阻塞当前 Goroutine
	<-ctx.Done()
	logger.Info("Shutting down workers")

	return nil
}

// runWorker is a long-running function that will continually call the
// processNextWorkItem function in order to read and process a message on the
// workqueue.
func (c *Controller) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem will read a single work item off the workqueue and
// attempt to process it, by calling the syncHandler.
func (c *Controller) processNextWorkItem(ctx context.Context) bool {
	// 从队列取出待处理对象
	obj, shutdown := c.workqueue.Get()
	logger := klog.FromContext(ctx)

	if shutdown {
		return false
	}

	// We wrap this block in a func so we can defer c.workqueue.Done.
	err := func() error {
		// We call Done here so the workqueue knows we have finished
		// processing this item. We also must remember to call Forget if we
		// do not want this work item being re-queued. For example, we do
		// not call Forget if a transient error occurs, instead the item is
		// put back on the workqueue and attempted again after a back-off
		// period.
		defer c.workqueue.Done(obj)
		// Run the syncHandler, passing it the namespace/name string of the
		// Foo resource to be synced.
		// 调用 syncHandler 处理
		if err := c.syncHandler(ctx, obj); err != nil {
			// Put the item back on the workqueue to handle any transient errors.
			c.workqueue.AddRateLimited(obj)
			return fmt.Errorf("error syncing '%s': %s, requeuing", obj, err.Error())
		}
		// Finally, if no error occurs we Forget this item so it does not
		// get queued again until another change happens.
		c.workqueue.Forget(obj)
		logger.Info("Successfully synced", "resourceName", obj)
		return nil
	}()

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

// syncHandler compares the actual state with the desired, and attempts to
// converge the two. It then updates the Status block of the Foo resource
// with the current status of the resource.
func (c *Controller) syncHandler(ctx context.Context, key string) error {
	// Convert the namespace/name string into a distinct namespace and name
	logger := klog.LoggerWithValues(klog.FromContext(ctx), "resourceName", key)
	// 根据 namespace/name 获取 foo 资源
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	// Get the Foo resource with this namespace/name
	demo, err := c.demosLister.Demos(namespace).Get(name)
	if err != nil {
		// The Foo resource may no longer exist, in which case we stop
		// processing.
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("foo '%s' in work queue no longer exists", key))
			return nil
		}
		return err
	}
	if demo.Status.CopyStatus == "" || demo.Status.CopyStatus == "Failed" {
		// 这里应该负责创建 Copy Pod， 并修改 status 状态
		// Return and don't process any more work until the pod is Running
		// 创建pod
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "copy-pod",
				Namespace: demo.Namespace,
				OwnerReferences: []metav1.OwnerReference{
					*metav1.NewControllerRef(demo, samplev1alpha1.SchemeGroupVersion.WithKind("Demo")),
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "demo-container",
						Image: demo.Spec.FileImage,
						Command: []string{
							"/bin/sh", "-c",
							fmt.Sprintf("cp %s %s", demo.Spec.SourcePath, demo.Spec.TargetPath),
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "host-volume",
								MountPath: "/mnt", //将主机目录挂载到容器内的 /build/ 路径
							},
						},
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "host-volume",
						VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{
								Path: "/mnt",
							},
						},
					},
				},
				NodeName: demo.Spec.TargetHost,
			},
		}
		myPod, err := c.kubeclientset.CoreV1().Pods(demo.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
		if err != nil {
			//demo.Status.CopyStatus = "Failed"
			err = c.updateFooStatus(demo, "Failed")
			if err != nil {
				return err
			}
			return err
		}
		logger.V(4).Info("Pod created", "podName", myPod.Name)
		//demo.Status.CopyStatus = "complete"
		err = c.updateFooStatus(demo, "Completed")

		if err != nil {
			return err
		}
		// 记录事件，记录一个正常（Normal）类型的事件，类型为 SuccessSynced，消息为 MessageResourceSynced
		c.recorder.Event(demo, corev1.EventTypeNormal, SuccessSynced, MessageResourceSynced)
	}

	if demo.Status.CopyStatus == "Completed" {
		// 回收pod
		err = c.kubeclientset.CoreV1().Pods(demo.Namespace).Delete(context.TODO(), "copy-pod", metav1.DeleteOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				// Pod 不存在，从队列中移除工作项
				c.workqueue.Forget(key)
				return nil
			}
			return err
		}
		// 记录事件，记录一个正常（Normal）类型的事件，类型为 SuccessSynced，消息为 MessageResourceSynced
		c.recorder.Event(demo, corev1.EventTypeNormal, SuccessSynced, MessageResourceSynced)
		// 移除
		c.workqueue.Forget(key)
		logger.V(4).Info("Pod deleted")
		return nil
	}
	return nil
}

func (c *Controller) updateFooStatus(demo *samplev1alpha1.Demo, status string) error {
	// NEVER modify objects from the store. It's a read-only, local cache.
	// You can use DeepCopy() to make a deep copy of original object and modify this copy
	// Or create a copy manually for better performance
	fooCopy := demo.DeepCopy()
	fooCopy.Status.CopyStatus = status
	fooCopy.Status.CopyDescription = "file copy " + status
	// If the CustomResourceSubresources feature gate is not enabled,
	// we must use Update instead of UpdateStatus to update the Status block of the Foo resource.
	// UpdateStatus will not allow changes to the Spec of the resource,
	// which is ideal for ensuring nothing other than resource status has been updated.
	_, err := c.sampleclientset.SamplecontrollerV1alpha1().Demos(demo.Namespace).Update(context.TODO(), fooCopy, metav1.UpdateOptions{})
	return err
}

// enqueueFoo takes a Foo resource and converts it into a namespace/name
// string which is then put onto the work queue. This method should *not* be
// passed resources of any type other than Foo.
// 解析 Foo 资源为 namespace/name 形式的字符串，然后入队
func (c *Controller) enqueueFoo(obj interface{}) {
	var key string
	var err error
	// 从给定的资源对象 obj 中获取元数据并生成一个唯一的键
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.workqueue.Add(key)
}

// handleObject will take any resource implementing metav1.Object and attempt
// to find the Foo resource that 'owns' it. It does this by looking at the
// objects metadata.ownerReferences field for an appropriate OwnerReference.
// It then enqueues that Foo resource to be processed. If the object does not
// have an appropriate OwnerReference, it will simply be skipped.
// 监听了所有实现了 metav1 的资源，但只过滤出 owner 是 Foo 的， 将其解析为 namespace/name 入队
func (c *Controller) handleObject(obj interface{}) {
	var object metav1.Object
	var ok bool
	logger := klog.FromContext(context.Background())
	if object, ok = obj.(metav1.Object); !ok {
		//进入这里说明obj不是一个正常的资源对象
		//如果 obj 不是一个正常的资源对象，代码会尝试将其转换为 cache.DeletedFinalStateUnknown 类型。
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		// 如果成功说明 obj 是一个被删除的资源对象的墓碑状态。
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object, invalid type"))
			return
		}
		object, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object tombstone, invalid type"))
			return
		}
		// 如果转换成功，表示成功恢复了被删除的对象
		logger.V(4).Info("Recovered deleted object", "resourceName", object.GetName())
	}
	logger.V(4).Info("Processing object", "object", klog.KObj(object))
	if ownerRef := metav1.GetControllerOf(object); ownerRef != nil {
		// If this object is not owned by a Foo, we should not do anything more
		// with it.
		if ownerRef.Kind != "Demo" {
			return
		}

		demo, err := c.demosLister.Demos(object.GetNamespace()).Get(ownerRef.Name)
		if err != nil {
			logger.V(4).Info("Ignore orphaned object", "object", klog.KObj(object), "demo", ownerRef.Name)
			return
		}

		c.enqueueFoo(demo)
		return
	}
}

// newDeployment creates a new Deployment for a Foo resource. It also sets
// the appropriate OwnerReferences on the resource so handleObject can discover
// the Foo resource that 'owns' it.
// newDeployment为Foo资源创建一个新的Deployment。
// 它还在资源上设置适当的OwnerReferences，因为 handleObject 中的筛选 Foo 资源代码是根据 Kind 值做的
// 以便handleObject可以发现“拥有”它的Foo资源。
//func newDeployment(demo *samplev1alpha1.Demo) *appsv1.Deployment {
//	labels := map[string]string{
//		"app":        "demo-Deployment",
//		"controller": demo.Name,
//	}
//	return &appsv1.Deployment{
//		ObjectMeta: metav1.ObjectMeta{
//			Name:      demo.Spec.DeploymentName,
//			Namespace: demo.Namespace,
//			OwnerReferences: []metav1.OwnerReference{
//				*metav1.NewControllerRef(demo, samplev1alpha1.SchemeGroupVersion.WithKind("Demo")),
//			},
//		},
//		Spec: appsv1.DeploymentSpec{
//			Replicas: demo.Spec.Replicas,
//			Selector: &metav1.LabelSelector{
//				MatchLabels: labels,
//			},
//			Template: corev1.PodTemplateSpec{
//				ObjectMeta: metav1.ObjectMeta{
//					Labels: labels,
//				},
//				Spec: corev1.PodSpec{
//					Volumes: []corev1.Volume{
//						{
//							Name: "host-volume", //卷的名称，必须与 VolumeMounts 中的名称匹配。
//							VolumeSource: corev1.VolumeSource{
//								HostPath: &corev1.HostPathVolumeSource{
//									Path: "/mnt",
//								},
//							},
//						},
//					},
//					Containers: []corev1.Container{
//						{
//							Name:  "demo-containers",
//							Image: demo.Spec.FileImage,
//							Command: []string{
//								"/bin/sh", "-c",
//								fmt.Sprintf("cp %s /build%s", demo.Spec.SourcePath, demo.Spec.TargetPath),
//							},
//							VolumeMounts: []corev1.VolumeMount{
//								{
//									Name:      "host-volume",
//									MountPath: "/build", //将主机目录挂载到容器内的 /mnt/host 路径
//								},
//							},
//						},
//					},
//					//NodeSelector: map[string]string{
//					//	"ip": "9.134.236.167",cd
//					//},
//					// 注：此处添加了主机目录挂载卷的定义。
//
//				},
//			},
//		},
//	}
//}
