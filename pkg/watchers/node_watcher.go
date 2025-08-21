package watchers

import (
	"context"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	klog "k8s.io/klog/v2"

	"github.com/garvinp/karpenter-helper/pkg/metrics"
)

const (
	KarpenterNodePoolLabel    = "karpenter.sh/nodepool"
	KarpenterProvisionerLabel = "karpenter.sh/provisioner"
	KarpenterTTLAnnotation    = "karpenter.sh/ttl"
	NominatedNodeAnnotation   = "scheduler.alpha.kubernetes.io/nominated-node"

	// TTL reason constants
	TTLReasonExpired       = "ttl_expired"
	TTLReasonEarlyDeletion = "early_deletion"
	TTLReasonConfigured    = "ttl_configured"
)

type NodeState struct {
	Node             *corev1.Node
	CreationTime     time.Time
	ExpectedDeletion *time.Time
	NominatedPods    map[string]string // podName -> namespace
}

type NodeWatcher struct {
	clientset *kubernetes.Clientset
	collector *metrics.Collector

	mu    sync.RWMutex
	nodes map[string]*NodeState
}

func NewNodeWatcher(clientset *kubernetes.Clientset, collector *metrics.Collector) (*NodeWatcher, error) {
	return &NodeWatcher{
		clientset: clientset,
		collector: collector,
		nodes:     make(map[string]*NodeState),
	}, nil
}

func (nw *NodeWatcher) Run(ctx context.Context) {
	klog.Info("Starting node watcher")

	watchlist := &metav1.ListOptions{
		FieldSelector: fields.Everything().String(),
	}

	watcher, err := nw.clientset.CoreV1().Nodes().Watch(ctx, *watchlist)
	if err != nil {
		klog.Errorf("Failed to create node watcher: %v", err)
		return
	}
	defer watcher.Stop()

	for event := range watcher.ResultChan() {
		switch event.Type {
		case watch.Added:
			nw.handleNodeAdded(event.Object.(*corev1.Node))
		case watch.Modified:
			nw.handleNodeModified(event.Object.(*corev1.Node))
		case watch.Deleted:
			nw.handleNodeDeleted(event.Object.(*corev1.Node))
		}
	}
}

func (nw *NodeWatcher) handleNodeAdded(node *corev1.Node) {
	nw.mu.Lock()
	defer nw.mu.Unlock()

	nodeState := &NodeState{
		Node:          node,
		CreationTime:  node.CreationTimestamp.Time,
		NominatedPods: make(map[string]string),
	}

	// Check if this node was created for a nominated pod
	nw.checkNominatedPodCreation(node, nodeState)

	// Parse TTL if present
	if ttlStr, exists := node.Annotations[KarpenterTTLAnnotation]; exists {
		if ttlDuration, err := time.ParseDuration(ttlStr); err == nil {
			expectedDeletion := nodeState.CreationTime.Add(ttlDuration)
			nodeState.ExpectedDeletion = &expectedDeletion
		}
	}

	nw.nodes[node.Name] = nodeState
}

func (nw *NodeWatcher) handleNodeModified(node *corev1.Node) {
	nw.mu.Lock()
	defer nw.mu.Unlock()

	if nodeState, exists := nw.nodes[node.Name]; exists {
		nodeState.Node = node

		// Check for TTL changes
		if ttlStr, exists := node.Annotations[KarpenterTTLAnnotation]; exists {
			if ttlDuration, err := time.ParseDuration(ttlStr); err == nil {
				expectedDeletion := nodeState.CreationTime.Add(ttlDuration)
				nodeState.ExpectedDeletion = &expectedDeletion
			}
		}
	}
}

func (nw *NodeWatcher) handleNodeDeleted(node *corev1.Node) {
	nw.mu.Lock()
	defer nw.mu.Unlock()

	if nodeState, exists := nw.nodes[node.Name]; exists {
		// Check if this was a TTL-driven deletion
		nw.checkTTLDeletion(node, nodeState)

		delete(nw.nodes, node.Name)
	}
}

func (nw *NodeWatcher) checkNominatedPodCreation(node *corev1.Node, nodeState *NodeState) {
	// Check if this node has Karpenter labels indicating it was provisioned for specific workloads
	nodepool := node.Labels[KarpenterNodePoolLabel]
	if nodepool == "" {
		nodepool = node.Labels[KarpenterProvisionerLabel] // fallback for older versions
	}

	if nodepool != "" {
		// This is a Karpenter-managed node, record its creation
		klog.V(2).Infof("Karpenter node %s created in nodepool %s", node.Name, nodepool)

		// We'll track nominated pods when we see pod events
		// For now, just record that a node was created that might be for nominations
	}
}

func (nw *NodeWatcher) checkTTLDeletion(node *corev1.Node, nodeState *NodeState) {
	nodepool := node.Labels[KarpenterNodePoolLabel]
	if nodepool == "" {
		nodepool = node.Labels[KarpenterProvisionerLabel]
	}

	// Determine if this was likely a TTL deletion
	reason := "unknown"
	if nodeState.ExpectedDeletion != nil {
		if time.Now().After(*nodeState.ExpectedDeletion) {
			reason = TTLReasonExpired
		} else {
			reason = TTLReasonEarlyDeletion
		}
	} else if _, hasTTL := node.Annotations[KarpenterTTLAnnotation]; hasTTL {
		reason = TTLReasonConfigured
	}

	if nodepool != "" && (reason == TTLReasonExpired || reason == TTLReasonConfigured) {
		nw.collector.RecordNodeTTLDeath(node.Name, nodepool, reason)
		klog.V(2).Infof("Recorded TTL death for node %s in nodepool %s, reason: %s", node.Name, nodepool, reason)
	}
}

func (nw *NodeWatcher) CheckUnreapedNodes() {
	nw.mu.RLock()
	defer nw.mu.RUnlock()

	now := time.Now()

	for nodeName, nodeState := range nw.nodes {
		// Check if node should have been reaped due to TTL
		if nodeState.ExpectedDeletion != nil && now.After(*nodeState.ExpectedDeletion) {
			// Node is past its TTL but still exists
			delay := now.Sub(*nodeState.ExpectedDeletion).Seconds()

			nodepool := nodeState.Node.Labels[KarpenterNodePoolLabel]
			if nodepool == "" {
				nodepool = nodeState.Node.Labels[KarpenterProvisionerLabel]
			}

			// Check if it has nominated pods preventing reaping
			if len(nodeState.NominatedPods) > 0 {
				for podName, namespace := range nodeState.NominatedPods {
					nw.collector.SetNodeNotReapedDueToNomination(nodeName, nodepool, podName, namespace, 1)
				}
			} else {
				nw.collector.RecordNodeReapingDelay(nodeName, nodepool, "ttl_overdue", delay)
			}
		}
	}
}

func (nw *NodeWatcher) AddNominatedPod(nodeName, podName, namespace string) {
	nw.mu.Lock()
	defer nw.mu.Unlock()

	if nodeState, exists := nw.nodes[nodeName]; exists {
		nodeState.NominatedPods[podName] = namespace

		// Record metrics for node created for nominated pod
		nodepool := nodeState.Node.Labels[KarpenterNodePoolLabel]
		if nodepool == "" {
			nodepool = nodeState.Node.Labels[KarpenterProvisionerLabel]
		}

		if nodepool != "" {
			nw.collector.RecordNodeCreatedForNominatedPod(nodeName, podName, namespace, nodepool)
		}
	}
}

func (nw *NodeWatcher) RemoveNominatedPod(nodeName, podName string) {
	nw.mu.Lock()
	defer nw.mu.Unlock()

	if nodeState, exists := nw.nodes[nodeName]; exists {
		delete(nodeState.NominatedPods, podName)
	}
}
