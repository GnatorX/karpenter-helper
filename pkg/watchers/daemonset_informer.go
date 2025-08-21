package watchers

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	klog "k8s.io/klog/v2"

	"github.com/garvinp/karpenter-helper/pkg/metrics"
)

// DaemonsetInformer watches daemonsets using informers
type DaemonsetInformer struct {
	clientset *kubernetes.Clientset
	collector *metrics.Collector
	informer  cache.SharedInformer
	stopCh    chan struct{}
}

// NewDaemonsetInformer creates a new daemonset informer
func NewDaemonsetInformer(clientset *kubernetes.Clientset, collector *metrics.Collector) (*DaemonsetInformer, error) {
	di := &DaemonsetInformer{
		clientset: clientset,
		collector: collector,
		stopCh:    make(chan struct{}),
	}

	// Create the informer
	di.informer = cache.NewSharedInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return clientset.AppsV1().DaemonSets("").List(context.TODO(), options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return clientset.AppsV1().DaemonSets("").Watch(context.TODO(), options)
			},
		},
		&appsv1.DaemonSet{},
		0, // No resync period
	)

	// Set up event handlers
	di.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    di.onDaemonsetAdd,
		UpdateFunc: di.onDaemonsetUpdate,
		DeleteFunc: di.onDaemonsetDelete,
	})

	return di, nil
}

// Start starts the informer
func (di *DaemonsetInformer) Start(ctx context.Context) error {
	go di.informer.Run(ctx.Done())
	return nil
}

// Stop stops the informer
func (di *DaemonsetInformer) Stop() {
	close(di.stopCh)
}

// HasSynced returns true if the informer has synced
func (di *DaemonsetInformer) HasSynced() bool {
	return di.informer.HasSynced()
}

// onDaemonsetAdd handles daemonset addition events
func (di *DaemonsetInformer) onDaemonsetAdd(obj interface{}) {
	ds, ok := obj.(*appsv1.DaemonSet)
	if !ok {
		klog.Errorf("Expected *appsv1.DaemonSet, got %T", obj)
		return
	}
	klog.V(2).Infof("DaemonSet added: %s/%s", ds.Namespace, ds.Name)
}

// onDaemonsetUpdate handles daemonset update events
func (di *DaemonsetInformer) onDaemonsetUpdate(oldObj, newObj interface{}) {
	ds, ok := newObj.(*appsv1.DaemonSet)
	if !ok {
		klog.Errorf("Expected *appsv1.DaemonSet, got %T", newObj)
		return
	}
	klog.V(2).Infof("DaemonSet updated: %s/%s", ds.Namespace, ds.Name)
}

// onDaemonsetDelete handles daemonset deletion events
func (di *DaemonsetInformer) onDaemonsetDelete(obj interface{}) {
	ds, ok := obj.(*appsv1.DaemonSet)
	if !ok {
		klog.Errorf("Expected *appsv1.DaemonSet, got %T", obj)
		return
	}
	klog.V(2).Infof("DaemonSet deleted: %s/%s", ds.Namespace, ds.Name)
}
