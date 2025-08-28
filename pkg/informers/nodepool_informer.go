package informers

import (
	"context"
	"fmt"
	"sync"
	"time"

	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	klog "k8s.io/klog/v2"

	"github.com/garvinp/karpenter-helper/pkg/metrics"
)

// NodePoolInformer watches Karpenter NodePool resources using informers
type NodePoolInformer struct {
	clientset *kubernetes.Clientset
	collector *metrics.Collector

	// Informer and cache
	informer cache.SharedInformer

	// State tracking
	nodepoolStates map[string]*NodePoolState
	mu             sync.RWMutex

	// Control
	stopCh chan struct{}
}

// NodePoolState tracks the state of a NodePool in the informer
type NodePoolState struct {
	Name        string
	APIVersion  string
	Kind        string
	Spec        karpenterv1.NodePoolSpec
	Status      karpenterv1.NodePoolStatus
	LastSeen    time.Time
	ExpectedTTL *time.Duration

	// Requirements extracted from the NodePool spec
	Requirements scheduling.Requirements
	Taints       []corev1.Taint
}

// convertToNodePool converts an unstructured object to a typed NodePool
func convertToNodePool(obj interface{}) (*karpenterv1.NodePool, error) {
	unstructuredObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return nil, fmt.Errorf("expected *unstructured.Unstructured, got %T", obj)
	}

	nodePool := &karpenterv1.NodePool{}
	err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredObj.Object, nodePool)
	if err != nil {
		return nil, fmt.Errorf("failed to convert unstructured to NodePool: %w", err)
	}

	return nodePool, nil
}

// NewNodePoolInformer creates a new NodePool informer
func NewNodePoolInformer(ctx context.Context, dynamicInformerFactory dynamicinformer.DynamicSharedInformerFactory, collector *metrics.Collector) (*NodePoolInformer, error) {
	npi := &NodePoolInformer{
		collector:      collector,
		nodepoolStates: make(map[string]*NodePoolState),
		stopCh:         make(chan struct{}),
	}

	// Get the generic informer for NodePools using the dynamic informer factory
	// Karpenter NodePools are in the karpenter.sh group
	groupVersion := schema.GroupVersion{Group: "karpenter.sh", Version: "v1"}
	genericInformer := dynamicInformerFactory.ForResource(groupVersion.WithResource("nodepools"))
	npi.informer = genericInformer.Informer()

	// Set up event handlers
	npi.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    npi.onNodePoolAdd,
		UpdateFunc: npi.onNodePoolUpdate,
		DeleteFunc: npi.onNodePoolDelete,
	})

	return npi, nil
}

// Start starts the informer
func (npi *NodePoolInformer) Start(ctx context.Context) error {
	go npi.informer.Run(ctx.Done())

	// Wait for the informer to sync and then process existing NodePools
	go func() {
		if cache.WaitForCacheSync(ctx.Done(), npi.informer.HasSynced) {
			npi.processExistingNodePools()
		}
	}()

	return nil
}

// Stop stops the informer
func (npi *NodePoolInformer) Stop() {
	close(npi.stopCh)
}

// HasSynced returns true if the informer has synced
func (npi *NodePoolInformer) HasSynced() bool {
	return npi.informer.HasSynced()
}

// parseNodePoolRequirements extracts requirements and taints from a NodePool spec
func (npi *NodePoolInformer) parseNodePoolRequirements(nodePool *karpenterv1.NodePool) (scheduling.Requirements, []corev1.Taint) {
	var requirements scheduling.Requirements
	var taints []corev1.Taint

	// Parse requirements from NodePool spec template
	if nodePool.Spec.Template.Spec.Requirements != nil {
		// Use the built-in function to convert NodeSelectorRequirements to scheduling.Requirements
		requirements = scheduling.NewNodeSelectorRequirementsWithMinValues(nodePool.Spec.Template.Spec.Requirements...)
	}

	// Extract taints from the template spec
	if nodePool.Spec.Template.Spec.Taints != nil {
		taints = nodePool.Spec.Template.Spec.Taints
	}

	return requirements, taints
}

// onNodePoolAdd handles NodePool addition events
func (npi *NodePoolInformer) onNodePoolAdd(obj interface{}) {
	nodePool, err := convertToNodePool(obj)
	if err != nil {
		klog.Errorf("Failed to convert NodePool: %v", err)
		return
	}

	npi.mu.Lock()
	defer npi.mu.Unlock()

	// Parse requirements from the NodePool spec
	requirements, taints := npi.parseNodePoolRequirements(nodePool)

	// Create NodePool state
	state := &NodePoolState{
		Name:         nodePool.Name,
		APIVersion:   nodePool.APIVersion,
		Kind:         nodePool.Kind,
		Spec:         nodePool.Spec,
		Status:       nodePool.Status,
		LastSeen:     time.Now(),
		Requirements: requirements,
		Taints:       taints,
	}

	// Extract TTL information if present
	if nodePool.Spec.Disruption.ConsolidateAfter.Duration != nil {
		state.ExpectedTTL = nodePool.Spec.Disruption.ConsolidateAfter.Duration
	}

	npi.nodepoolStates[nodePool.Name] = state
	reqCount := 0
	if requirements != nil {
		reqCount = len(requirements)
	}
	klog.V(2).Infof("Added NodePool %s to tracking with %d requirements", nodePool.Name, reqCount)
}

// onNodePoolUpdate handles NodePool update events
func (npi *NodePoolInformer) onNodePoolUpdate(oldObj, newObj interface{}) {
	newNodePool, err := convertToNodePool(newObj)
	if err != nil {
		klog.Errorf("Failed to convert updated NodePool: %v", err)
		return
	}

	npi.mu.Lock()
	defer npi.mu.Unlock()

	state, exists := npi.nodepoolStates[newNodePool.Name]
	if !exists {
		klog.Warningf("NodePool %s not found in tracking state", newNodePool.Name)
		return
	}

	// Update state information
	state.Spec = newNodePool.Spec
	state.Status = newNodePool.Status
	state.LastSeen = time.Now()

	// Update TTL information if changed
	if newNodePool.Spec.Disruption.ConsolidateAfter.Duration != nil {
		state.ExpectedTTL = newNodePool.Spec.Disruption.ConsolidateAfter.Duration
	} else {
		state.ExpectedTTL = nil
	}

	klog.V(2).Infof("Updated NodePool %s tracking", newNodePool.Name)
}

// onNodePoolDelete handles NodePool deletion events
func (npi *NodePoolInformer) onNodePoolDelete(obj interface{}) {
	nodePool, err := convertToNodePool(obj)
	if err != nil {
		klog.Errorf("Failed to convert deleted NodePool: %v", err)
		return
	}

	npi.mu.Lock()
	defer npi.mu.Unlock()

	// Remove from tracking
	delete(npi.nodepoolStates, nodePool.Name)
	klog.V(2).Infof("Removed NodePool %s from tracking", nodePool.Name)
}

// processExistingNodePools processes all existing NodePools once the informer has synced
func (npi *NodePoolInformer) processExistingNodePools() {
	klog.Info("Processing existing NodePools after informer sync")

	// Get all existing NodePools from the informer cache
	nodepools := npi.informer.GetStore().List()

	for _, obj := range nodepools {
		if nodePool, err := convertToNodePool(obj); err == nil {
			npi.onNodePoolAdd(nodePool)
		} else {
			klog.Errorf("Failed to convert existing NodePool: %v", err)
		}
	}

	klog.Infof("Processed %d existing NodePools", len(npi.nodepoolStates))
}

// GetNodePoolState returns the current state of a NodePool
func (npi *NodePoolInformer) GetNodePoolState(nodepoolName string) (*NodePoolState, bool) {
	npi.mu.RLock()
	defer npi.mu.RUnlock()

	state, exists := npi.nodepoolStates[nodepoolName]
	return state, exists
}

// ListNodePools returns all tracked NodePools
func (npi *NodePoolInformer) ListNodePools() []*NodePoolState {
	npi.mu.RLock()
	defer npi.mu.RUnlock()

	nodepools := make([]*NodePoolState, 0, len(npi.nodepoolStates))
	for _, state := range npi.nodepoolStates {
		nodepools = append(nodepools, state)
	}
	return nodepools
}

// GetNodePoolByName returns a NodePool by name
func (npi *NodePoolInformer) GetNodePoolByName(name string) (*karpenterv1.NodePool, bool) {
	npi.mu.RLock()
	defer npi.mu.RUnlock()

	obj, exists, err := npi.informer.GetStore().GetByKey(name)
	if err != nil || !exists {
		return nil, false
	}

	if nodePool, ok := obj.(*karpenterv1.NodePool); ok {
		return nodePool, true
	}

	return nil, false
}
