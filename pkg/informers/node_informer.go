package informers

import (
	"context"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	klog "k8s.io/klog/v2"

	"github.com/garvinp/karpenter-helper/pkg/metrics"
)

// Karpenter-related constants
const (
	KarpenterNodePoolLabel    = "karpenter.sh/nodepool"
	KarpenterProvisionerLabel = "karpenter.sh/provisioner"
	KarpenterTTLAnnotation    = "karpenter.sh/ttl"

	// TTL reason constants
	TTLReasonExpired       = "ttl_expired"
	TTLReasonEarlyDeletion = "early_deletion"
	TTLReasonConfigured    = "ttl_configured"
)

// NodeInformer watches nodes using informers and detects TTL-related events
type NodeInformer struct {
	clientset *kubernetes.Clientset
	collector *metrics.Collector

	// Informer and cache
	informer cache.SharedInformer

	// State tracking
	nodeStates map[string]*NodeInformerState
	mu         sync.RWMutex

	// Control
	stopCh chan struct{}
}

// NodeInformerState tracks the state of a node in the informer
type NodeInformerState struct {
	Name             string
	Nodepool         string
	TTLAnnotation    string
	ExpectedDeletion *time.Time
	LastSeen         time.Time
}

// NewNodeInformer creates a new node informer
func NewNodeInformer(ctx context.Context, sharedInformerFactory informers.SharedInformerFactory, collector *metrics.Collector) (*NodeInformer, error) {
	ni := &NodeInformer{
		collector:  collector,
		nodeStates: make(map[string]*NodeInformerState),
		stopCh:     make(chan struct{}),
	}
	// Create the informer
	ni.informer = sharedInformerFactory.Core().V1().Nodes().Informer()

	// Set up event handlers
	ni.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    ni.onNodeAdd,
		UpdateFunc: ni.onNodeUpdate,
		DeleteFunc: ni.onNodeDelete,
	})

	return ni, nil
}

// Start starts the informer
func (ni *NodeInformer) Start(ctx context.Context) error {
	go ni.informer.Run(ctx.Done())

	// Wait for the informer to sync and then process existing nodes
	go func() {
		if cache.WaitForCacheSync(ctx.Done(), ni.informer.HasSynced) {
			ni.processExistingNodes()
		}
	}()

	return nil
}

// Stop stops the informer
func (ni *NodeInformer) Stop() {
	close(ni.stopCh)
}

// HasSynced returns true if the informer has synced
func (ni *NodeInformer) HasSynced() bool {
	return ni.informer.HasSynced()
}

// onNodeAdd handles node addition events
func (ni *NodeInformer) onNodeAdd(obj interface{}) {
	node, ok := obj.(*corev1.Node)
	if !ok {
		klog.Errorf("Expected *corev1.Node, got %T", obj)
		return
	}

	ni.mu.Lock()
	defer ni.mu.Unlock()

	// Create node state
	state := &NodeInformerState{
		Name:     node.Name,
		LastSeen: time.Now(),
	}

	// Extract nodepool information
	if nodepool, exists := node.Labels[KarpenterNodePoolLabel]; exists {
		state.Nodepool = nodepool
	} else if provisioner, exists := node.Labels[KarpenterProvisionerLabel]; exists {
		state.Nodepool = provisioner
	}

	// Extract TTL information
	if ttl, exists := node.Annotations[KarpenterTTLAnnotation]; exists {
		state.TTLAnnotation = ttl
		if duration, err := time.ParseDuration(ttl); err == nil {
			expectedDeletion := time.Now().Add(duration)
			state.ExpectedDeletion = &expectedDeletion
		}
	}

	ni.nodeStates[node.Name] = state
	klog.V(2).Infof("Added node %s to tracking", node.Name)
}

// onNodeUpdate handles node update events
func (ni *NodeInformer) onNodeUpdate(oldObj, newObj interface{}) {
	newNode, ok := newObj.(*corev1.Node)
	if !ok {
		klog.Errorf("Expected *corev1.Node, got %T", newObj)
		return
	}

	ni.mu.Lock()
	defer ni.mu.Unlock()

	state, exists := ni.nodeStates[newNode.Name]
	if !exists {
		klog.Warningf("Node %s not found in tracking state", newNode.Name)
		return
	}

	// Update TTL information if changed
	if ttl, exists := newNode.Annotations[KarpenterTTLAnnotation]; exists && ttl != state.TTLAnnotation {
		state.TTLAnnotation = ttl
		if duration, err := time.ParseDuration(ttl); err == nil {
			expectedDeletion := time.Now().Add(duration)
			state.ExpectedDeletion = &expectedDeletion
		}
	}

	state.LastSeen = time.Now()
	klog.V(2).Infof("Updated node %s tracking", newNode.Name)
}

// onNodeDelete handles node deletion events
func (ni *NodeInformer) onNodeDelete(obj interface{}) {
	node, ok := obj.(*corev1.Node)
	if !ok {
		klog.Errorf("Expected *corev1.Node, got %T", obj)
		return
	}

	ni.mu.Lock()
	defer ni.mu.Unlock()

	state, exists := ni.nodeStates[node.Name]
	if !exists {
		klog.Warningf("Node %s not found in tracking state", node.Name)
		return
	}

	// Check if this was a TTL deletion
	ni.checkTTLDeletion(node, state)

	// Remove from tracking
	delete(ni.nodeStates, node.Name)
	klog.V(2).Infof("Removed node %s from tracking", node.Name)
}

// checkTTLDeletion determines if a node deletion was TTL-related
func (ni *NodeInformer) checkTTLDeletion(node *corev1.Node, nodeState *NodeInformerState) {
	nodepool := nodeState.Nodepool
	if nodepool == "" {
		return
	}

	// Determine if this was likely a TTL deletion
	reason := "unknown"
	if nodeState.ExpectedDeletion != nil {
		if time.Now().After(*nodeState.ExpectedDeletion) {
			reason = TTLReasonExpired
		} else {
			reason = TTLReasonEarlyDeletion
		}
	} else if nodeState.TTLAnnotation != "" {
		reason = TTLReasonConfigured
	}

	if reason == TTLReasonExpired || reason == TTLReasonConfigured {
		ni.collector.RecordNodeTTLDeath(node.Name, nodepool, reason)
		klog.V(2).Infof("Recorded TTL death for node %s in nodepool %s, reason: %s", node.Name, nodepool, reason)
	}
}

// CheckUnreapedNodes checks for nodes that should be reaped but aren't
func (ni *NodeInformer) CheckUnreapedNodes() {
	ni.mu.RLock()
	defer ni.mu.RUnlock()

	now := time.Now()
	for name, state := range ni.nodeStates {
		if state.ExpectedDeletion != nil && now.After(*state.ExpectedDeletion) {
			klog.V(2).Infof("Node %s should have been reaped by now", name)
			// Could emit metrics here for unreaped nodes
		}
	}
}

// GetNodeState returns the current state of a node
func (ni *NodeInformer) GetNodeState(nodeName string) (*NodeInformerState, bool) {
	ni.mu.RLock()
	defer ni.mu.RUnlock()

	state, exists := ni.nodeStates[nodeName]
	return state, exists
}

// ListNodes returns all tracked nodes
func (ni *NodeInformer) ListNodes() []*NodeInformerState {
	ni.mu.RLock()
	defer ni.mu.RUnlock()

	nodes := make([]*NodeInformerState, 0, len(ni.nodeStates))
	for _, state := range ni.nodeStates {
		nodes = append(nodes, state)
	}
	return nodes
}

// processExistingNodes processes all existing nodes once the informer has synced
func (ni *NodeInformer) processExistingNodes() {
	klog.Info("Processing existing nodes after informer sync")

	// Get all existing nodes from the informer cache
	nodes := ni.informer.GetStore().List()

	for _, obj := range nodes {
		if node, ok := obj.(*corev1.Node); ok {
			ni.onNodeAdd(node)
		}
	}

	klog.Infof("Processed %d existing nodes", len(nodes))
}
