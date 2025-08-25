package informers

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
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
	indexer   cache.Indexer
	stopCh    chan struct{}
}

// NewPodInformer creates a new pod informer
func NewPodInformer(ctx context.Context, clientset *kubernetes.Clientset, collector *metrics.Collector) (*PodInformer, error) {
	pi := &PodInformer{
		clientset: clientset,
		collector: collector,
		stopCh:    make(chan struct{}),
	}

	sharedInformerFactory := informers.NewSharedInformerFactory(clientset, 0)
	// Create the informer
	pi.informer = sharedInformerFactory.Core().V1().Pods().Informer()

	// Create an indexer with node-based indexing
	pi.indexer = cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{
		"node": func(obj interface{}) ([]string, error) {
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				return []string{}, nil
			}
			if pod.Spec.NodeName != "" {
				return []string{pod.Spec.NodeName}, nil
			}
			return []string{}, nil
		},
	})

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

// GetStore returns the indexer for accessing pod data
func (pi *PodInformer) GetStore() cache.Indexer {
	return pi.indexer
}

// GetPodsByNode returns all pods scheduled on a specific node
func (pi *PodInformer) GetPodsByNode(nodeName string) []*corev1.Pod {
	var pods []*corev1.Pod

	// Use the indexer to get pods by node
	podObjects, err := pi.indexer.ByIndex("node", nodeName)
	if err != nil {
		klog.Errorf("Failed to get pods by node %s: %v", nodeName, err)
		return pods
	}

	for _, obj := range podObjects {
		if pod, ok := obj.(*corev1.Pod); ok {
			pods = append(pods, pod)
		}
	}

	return pods
}

// onPodAdd handles pod addition events
func (pi *PodInformer) onPodAdd(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		klog.Errorf("Expected *corev1.Pod, got %T", obj)
		return
	}

	// Add to indexer
	if err := pi.indexer.Add(obj); err != nil {
		klog.Errorf("Failed to add pod to indexer: %v", err)
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

	// Update in indexer
	if err := pi.indexer.Update(newObj); err != nil {
		klog.Errorf("Failed to update pod in indexer: %v", err)
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

	// Remove from indexer
	if err := pi.indexer.Delete(obj); err != nil {
		klog.Errorf("Failed to delete pod from indexer: %v", err)
	}

	klog.V(2).Infof("Pod deleted: %s/%s", pod.Namespace, pod.Name)
}
