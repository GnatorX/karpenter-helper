package informers

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/client-go/informers"
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
	indexer   cache.Indexer
	stopCh    chan struct{}
}

// NewDaemonsetInformer creates a new daemonset informer
func NewDaemonsetInformer(ctx context.Context, sharedInformerFactory informers.SharedInformerFactory, collector *metrics.Collector) (*DaemonsetInformer, error) {
	di := &DaemonsetInformer{
		collector: collector,
		stopCh:    make(chan struct{}),
	}

	di.informer = sharedInformerFactory.Apps().V1().DaemonSets().Informer()

	// Create an indexer for efficient daemonset lookups
	di.indexer = cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{
		"name": func(obj interface{}) ([]string, error) {
			ds, ok := obj.(*appsv1.DaemonSet)
			if !ok {
				return []string{}, nil
			}
			return []string{ds.Name}, nil
		},
	})

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

// ListDaemonsets returns all tracked daemonsets
func (di *DaemonsetInformer) ListDaemonsets() []*appsv1.DaemonSet {
	daemonsets := make([]*appsv1.DaemonSet, 0)

	// Get all objects from the indexer
	objects := di.indexer.List()
	for _, obj := range objects {
		if ds, ok := obj.(*appsv1.DaemonSet); ok {
			daemonsets = append(daemonsets, ds)
		}
	}

	return daemonsets
}

// GetDaemonsetByName returns a daemonset by name from the indexer
func (di *DaemonsetInformer) GetDaemonsetByName(name string) (*appsv1.DaemonSet, bool) {
	// Search for daemonsets by name
	daemonsetObjects, err := di.indexer.ByIndex("name", name)
	if err != nil {
		klog.Errorf("Failed to get daemonset by name %s: %v", name, err)
		return nil, false
	}

	if len(daemonsetObjects) > 0 {
		if ds, ok := daemonsetObjects[0].(*appsv1.DaemonSet); ok {
			return ds, true
		}
	}

	return nil, false
}

// onDaemonsetAdd handles daemonset addition events
func (di *DaemonsetInformer) onDaemonsetAdd(obj interface{}) {
	ds, ok := obj.(*appsv1.DaemonSet)
	if !ok {
		klog.Errorf("Expected *appsv1.DaemonSet, got %T", obj)
		return
	}

	// Add to indexer
	if err := di.indexer.Add(obj); err != nil {
		klog.Errorf("Failed to add daemonset to indexer: %v", err)
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

	// Update in indexer
	if err := di.indexer.Update(newObj); err != nil {
		klog.Errorf("Failed to update daemonset in indexer: %v", err)
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

	// Remove from indexer
	if err := di.indexer.Delete(obj); err != nil {
		klog.Errorf("Failed to delete daemonset from indexer: %v", err)
	}

	klog.V(2).Infof("DaemonSet deleted: %s/%s", ds.Namespace, ds.Name)
}
