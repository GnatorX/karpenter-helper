package watchers

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	klog "k8s.io/klog/v2"

	"github.com/garvinp/karpenter-helper/pkg/metrics"
)

// PodInformer watches pods using informers
type PodInformer struct {
	clientset *kubernetes.Clientset
	collector *metrics.Collector
	informer  cache.SharedInformer
	stopCh    chan struct{}
}

// NewPodInformer creates a new pod informer
func NewPodInformer(clientset *kubernetes.Clientset, collector *metrics.Collector) (*PodInformer, error) {
	pi := &PodInformer{
		clientset: clientset,
		collector: collector,
		stopCh:    make(chan struct{}),
	}

	// Create the informer
	pi.informer = cache.NewSharedInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return clientset.CoreV1().Pods("").List(context.TODO(), options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return clientset.CoreV1().Pods("").Watch(context.TODO(), options)
			},
		},
		&corev1.Pod{},
		0, // No resync period
	)

	// Set up event handlers
	pi.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    pi.onPodAdd,
		UpdateFunc: pi.onPodUpdate,
		DeleteFunc: pi.onPodDelete,
	})

	return pi, nil
}

// Start starts the informer
func (pi *PodInformer) Start(ctx context.Context) error {
	go pi.informer.Run(ctx.Done())
	return nil
}

// Stop stops the informer
func (pi *PodInformer) Stop() {
	close(pi.stopCh)
}

// HasSynced returns true if the informer has synced
func (pi *PodInformer) HasSynced() bool {
	return pi.informer.HasSynced()
}

// onPodAdd handles pod addition events
func (pi *PodInformer) onPodAdd(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		klog.Errorf("Expected *corev1.Pod, got %T", obj)
		return
	}
	klog.V(2).Infof("Pod added: %s/%s", pod.Namespace, pod.Name)
}

// onPodUpdate handles pod update events
func (pi *PodInformer) onPodUpdate(oldObj, newObj interface{}) {
	pod, ok := newObj.(*corev1.Pod)
	if !ok {
		klog.Errorf("Expected *corev1.Pod, got %T", newObj)
		return
	}
	klog.V(2).Infof("Pod updated: %s/%s", pod.Namespace, pod.Name)
}

// onPodDelete handles pod deletion events
func (pi *PodInformer) onPodDelete(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		klog.Errorf("Expected *corev1.Pod, got %T", obj)
		return
	}
	klog.V(2).Infof("Pod deleted: %s/%s", pod.Namespace, pod.Name)
}
