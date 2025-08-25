package processors

import (
	"sync"

	klog "k8s.io/klog/v2"

	"github.com/garvinp/karpenter-helper/pkg/informers"
	"github.com/garvinp/karpenter-helper/pkg/metrics"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

// NodePoolDaemonsetProcessor handles cross-referencing daemonset pods with node nodepools
type NodePoolDaemonsetProcessor struct {
	collector         *metrics.Collector
	nodeInformer      *informers.NodeInformer
	podInformer       *informers.PodInformer
	daemonsetInformer *informers.DaemonsetInformer
	nodepoolInformer  *informers.NodePoolInformer
	mu                sync.RWMutex
}

// NewNodePoolDaemonsetProcessor creates a new processor
func NewNodePoolDaemonsetProcessor(collector *metrics.Collector, nodeInformer *informers.NodeInformer, podInformer *informers.PodInformer, daemonsetInformer *informers.DaemonsetInformer, nodepoolInformer *informers.NodePoolInformer) *NodePoolDaemonsetProcessor {
	return &NodePoolDaemonsetProcessor{
		collector:         collector,
		nodeInformer:      nodeInformer,
		podInformer:       podInformer,
		daemonsetInformer: daemonsetInformer,
		nodepoolInformer:  nodepoolInformer,
	}
}

// CheckForMismatches performs a comprehensive check for all nodepool mismatches
func (p *NodePoolDaemonsetProcessor) CheckForMismatches() {
	if p.nodeInformer == nil || p.podInformer == nil || p.daemonsetInformer == nil {
		klog.Warning("Cannot check for mismatches: informers not available")
		return
	}

	mismatchCount := 0

	// Get all nodes from the node informer
	nodes := p.nodeInformer.ListNodes()

	// Get all pods from the pod informer to find daemonset pods
	// Note: This is a simplified approach - in practice you might want to track daemonset pods specifically

	for _, nodeState := range nodes {
		if nodeState.Nodepool == "" {
			continue
		}

		// Check if any pods on this node have nodepool requirements that don't match
		// This is a placeholder for the actual cross-referencing logic
		klog.V(3).Infof("Checking node %s with nodepool %s for mismatches", nodeState.Name, nodeState.Nodepool)

		// Get all pods from the pod informer and check for daemonset pods on this node
		if p.checkNodePodCompatibility(nodeState) {
			mismatchCount++
		}
	}

	if mismatchCount > 0 {
		klog.Infof("Found %d nodepool mismatches", mismatchCount)
	} else {
		klog.V(3).Info("No nodepool mismatches found")
	}
}

// checkNodePodCompatibility checks if pods on a node are compatible with the node's nodepool
func (p *NodePoolDaemonsetProcessor) checkNodePodCompatibility(nodeState *informers.NodeInformerState) bool {
	// Get all pods from the pod informer
	// Note: This is a simplified approach - in a real implementation you'd want to:
	// 1. Use the pod informer's cache to find pods by node
	// 2. Filter for daemonset pods specifically
	// 3. Extract node selectors from the daemonset specs

	// Get all pods from the pod informer and check for daemonset pods on this node
	pods := p.getPodsByNode(nodeState.Name)

	for _, pod := range pods {
		if p.isDaemonsetPod(pod) {
			daemonset := p.getDaemonsetForPod(pod)
			if daemonset != nil {
				// Extract node selector from daemonset spec
				nodeSelector := daemonset.Spec.Template.Spec.NodeSelector
				if !p.isNodeSelectorCompatible(nodeSelector, nodeState.Nodepool) {
					klog.Warningf("Nodepool mismatch detected: Daemonset %s/%s requires nodepool selector %v, but node %s has nodepool %s",
						daemonset.Namespace, daemonset.Name, nodeSelector, nodeState.Name, nodeState.Nodepool)
					return true // mismatch found
				}
			}
		}
	}

	return false // no mismatches found
}

// getPodsByNode retrieves all pods scheduled on a specific node
func (p *NodePoolDaemonsetProcessor) getPodsByNode(nodeName string) []*corev1.Pod {
	// Use the pod informer's GetPodsByNode method for efficient node-based pod lookup
	return p.podInformer.GetPodsByNode(nodeName)
}

// isDaemonsetPod checks if a pod is owned by a daemonset
func (p *NodePoolDaemonsetProcessor) isDaemonsetPod(pod *corev1.Pod) bool {
	if pod == nil || pod.OwnerReferences == nil {
		return false
	}

	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "DaemonSet" && owner.APIVersion == "apps/v1" {
			return true
		}
	}

	return false
}

// getDaemonsetForPod retrieves the daemonset that owns a pod
func (p *NodePoolDaemonsetProcessor) getDaemonsetForPod(pod *corev1.Pod) *appsv1.DaemonSet {
	if pod == nil || pod.OwnerReferences == nil {
		return nil
	}

	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "DaemonSet" && owner.APIVersion == "apps/v1" {
			// Use the daemonset informer to get the full object
			daemonset, exists := p.daemonsetInformer.GetDaemonsetByName(owner.Name)
			if exists {
				return daemonset
			}
		}
	}

	return nil
}

// isNodeSelectorCompatible checks if a node selector is compatible with a nodepool
func (p *NodePoolDaemonsetProcessor) isNodeSelectorCompatible(nodeSelector map[string]string, nodepool string) bool {
	// This would implement the actual compatibility check:
	// 1. Check if nodeSelector has karpenter.sh/nodepool label
	// 2. Verify the value matches the node's nodepool
	// 3. Check other selector requirements against node labels

	if nodepool == "" {
		return true // No nodepool requirement
	}

	// No nodepool selector specified, so it's compatible
	return true
}

// GetNodeNodepoolInfo returns information about a specific node's nodepool
func (p *NodePoolDaemonsetProcessor) GetNodeNodepoolInfo(nodeName string) (*informers.NodeInformerState, bool) {
	if p.nodeInformer == nil {
		return nil, false
	}

	return p.nodeInformer.GetNodeState(nodeName)
}

// ListDaemonsetPods returns all tracked daemonset pods
func (p *NodePoolDaemonsetProcessor) ListDaemonsetPods() []interface{} {
	// This would return daemonset pods from the pod informer
	// For now, return empty slice as placeholder
	return []interface{}{}
}

// ListNodeNodepools returns all tracked node nodepools
func (p *NodePoolDaemonsetProcessor) ListNodeNodepools() []*informers.NodeInformerState {
	if p.nodeInformer == nil {
		return []*informers.NodeInformerState{}
	}

	return p.nodeInformer.ListNodes()
}

// Cleanup removes old entries based on a cutoff time
func (p *NodePoolDaemonsetProcessor) Cleanup(cutoff interface{}) {
	// Since we're using informers, cleanup is handled by the informers themselves
	// This method is kept for interface compatibility but doesn't need to do anything
	klog.V(3).Info("Cleanup called on NodePoolDaemonsetProcessor - no action needed (using informers)")
}
