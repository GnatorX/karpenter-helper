package processors

import (
	"fmt"

	klog "k8s.io/klog/v2"

	"github.com/garvinp/karpenter-helper/pkg/informers"
	"github.com/garvinp/karpenter-helper/pkg/metrics"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	appsv1 "k8s.io/api/apps/v1"
)

// NodePoolDaemonsetProcessor handles cross-referencing daemonset pods with node nodepools
type NodePoolDaemonsetProcessor struct {
	collector         *metrics.Collector
	nodeInformer      *informers.NodeInformer
	podInformer       *informers.PodInformer
	daemonsetInformer *informers.DaemonsetInformer
	nodepoolInformer  *informers.NodePoolInformer
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
	if p.nodeInformer == nil || p.podInformer == nil || p.daemonsetInformer == nil || p.nodepoolInformer == nil {
		klog.Warning("Cannot check for mismatches: informers not available")
		return
	}

	klog.Info("Starting comprehensive daemonset-nodepool compatibility check")

	// Get all nodes from the node informer
	nodes := p.nodeInformer.ListNodes()

	// Get all daemonsets from the daemonset informer
	daemonsets := p.daemonsetInformer.ListDaemonsets()

	// Get all NodePools from the nodepool informer
	nodepools := p.nodepoolInformer.ListNodePools()

	klog.Infof("Checking %d daemonsets against %d nodes across %d nodepools", len(daemonsets), len(nodes), len(nodepools))

	mismatchCount := 0
	compatibleCount := 0

	// Check each daemonset against each nodepool (using one representative node per nodepool)
	for _, daemonset := range daemonsets {
		daemonsetName := fmt.Sprintf("%s/%s", daemonset.Namespace, daemonset.Name)

		// Get daemonset's node selector requirements
		// Convert the daemonset's nodeSelector to Karpenter's scheduling.Requirements format
		var daemonsetRequirements scheduling.Requirements
		if daemonset.Spec.Template.Spec.NodeSelector != nil {
			daemonsetRequirements = scheduling.NewLabelRequirements(daemonset.Spec.Template.Spec.NodeSelector)
		}

		klog.V(2).Infof("Checking daemonset %s with requirements: %v", daemonsetName, daemonsetRequirements)

		// Create a map to track which nodepools we've already checked
		checkedNodepools := make(map[string]bool)

		// Check against each node, but only one per nodepool
		for _, nodeState := range nodes {
			if nodeState.Nodepool == "" {
				klog.V(3).Infof("Skipping node %s - no nodepool assigned", nodeState.Name)
				continue
			}

			// Skip if we've already checked this nodepool
			if checkedNodepools[nodeState.Nodepool] {
				klog.V(3).Infof("Skipping node %s - nodepool %s already checked", nodeState.Name, nodeState.Nodepool)
				continue
			}

			// Mark this nodepool as checked
			checkedNodepools[nodeState.Nodepool] = true

			// Get the NodePool state to check its requirements
			nodepoolState, exists := p.nodepoolInformer.GetNodePoolState(nodeState.Nodepool)
			if !exists {
				klog.Warningf("NodePool %s not found in informer state for node %s", nodeState.Nodepool, nodeState.Name)
				continue
			}

			// Check compatibility between daemonset and nodepool
			isCompatible := p.checkDaemonsetNodeCompatibility(daemonset, daemonsetRequirements, nodeState, nodepoolState)

			if isCompatible {
				compatibleCount++
				klog.V(3).Infof("✓ Daemonset %s is compatible with nodepool %s (checked via node: %s)",
					daemonsetName, nodeState.Nodepool, nodeState.Name)
			} else {
				mismatchCount++
				klog.Warningf("✗ Daemonset %s is NOT compatible with nodepool %s (checked via node: %s)",
					daemonsetName, nodeState.Nodepool, nodeState.Name)

				// Record the mismatch metric
				p.collector.RecordDaemonsetNodepoolMismatch(
					daemonset.Name,
					daemonset.Namespace,
					nodeState.Name,
					nodeState.Nodepool,
				)
			}
		}
	}

	klog.Infof("Compatibility check complete: %d compatible, %d mismatches found", compatibleCount, mismatchCount)

	if mismatchCount > 0 {
		klog.Warningf("Found %d daemonset-nodepool mismatches that need attention", mismatchCount)

		// Now check if any pods from incompatible daemonsets are actually running on incompatible nodepool nodes
		p.checkForRunningIncompatiblePods(daemonsets, nodes)
	} else {
		klog.Info("All daemonsets are compatible with their target nodepools")
	}
}

// checkForRunningIncompatiblePods checks if any pods from incompatible daemonsets are running on incompatible nodepool nodes
func (p *NodePoolDaemonsetProcessor) checkForRunningIncompatiblePods(daemonsets []*appsv1.DaemonSet, nodes []*informers.NodeInformerState) {
	klog.Info("Checking for pods from incompatible daemonsets running on incompatible nodepool nodes")

	// Get all pods from the pod informer
	pods := p.podInformer.ListPods()

	// Create a map of daemonset names to their nodepool compatibility status
	daemonsetNodepoolCompatibility := make(map[string]map[string]bool)

	// Build compatibility map for each daemonset against each nodepool
	for _, daemonset := range daemonsets {
		daemonsetKey := fmt.Sprintf("%s/%s", daemonset.Namespace, daemonset.Name)
		daemonsetNodepoolCompatibility[daemonsetKey] = make(map[string]bool)

		// Get daemonset's requirements
		var daemonsetRequirements scheduling.Requirements
		if daemonset.Spec.Template.Spec.NodeSelector != nil {
			daemonsetRequirements = scheduling.NewLabelRequirements(daemonset.Spec.Template.Spec.NodeSelector)
		}

		// Check compatibility with each nodepool
		checkedNodepools := make(map[string]bool)
		for _, nodeState := range nodes {
			if nodeState.Nodepool == "" || checkedNodepools[nodeState.Nodepool] {
				continue
			}
			checkedNodepools[nodeState.Nodepool] = true

			nodepoolState, exists := p.nodepoolInformer.GetNodePoolState(nodeState.Nodepool)
			if !exists {
				continue
			}

			// Check compatibility
			isCompatible := p.checkDaemonsetNodeCompatibility(daemonset, daemonsetRequirements, nodeState, nodepoolState)
			daemonsetNodepoolCompatibility[daemonsetKey][nodeState.Nodepool] = isCompatible
		}
	}

	// Now check each pod to see if it's running on an incompatible nodepool
	runningIncompatibleCount := 0
	for _, pod := range pods {
		// Check if this pod belongs to a daemonset
		if pod.OwnerReferences == nil {
			continue
		}

		var daemonsetName string
		var daemonsetNamespace string
		for _, owner := range pod.OwnerReferences {
			if owner.Kind == "DaemonSet" {
				daemonsetName = owner.Name
				daemonsetNamespace = pod.Namespace
				break
			}
		}

		if daemonsetName == "" {
			continue // Not a daemonset pod
		}

		// Find the node this pod is running on
		var podNode *informers.NodeInformerState
		for _, nodeState := range nodes {
			if nodeState.Name == pod.Spec.NodeName {
				podNode = nodeState
				break
			}
		}

		if podNode == nil || podNode.Nodepool == "" {
			continue // Pod not scheduled or node has no nodepool
		}

		// Check if this daemonset is incompatible with this nodepool
		daemonsetKey := fmt.Sprintf("%s/%s", daemonsetNamespace, daemonsetName)
		if nodepoolCompatibility, exists := daemonsetNodepoolCompatibility[daemonsetKey]; exists {
			if isCompatible, nodepoolExists := nodepoolCompatibility[podNode.Nodepool]; nodepoolExists && !isCompatible {
				runningIncompatibleCount++
				klog.Warningf("🚨 WARNNING: Pod %s/%s from incompatible daemonset %s is running on incompatible nodepool %s (node: %s)",
					pod.Namespace, pod.Name, daemonsetKey, podNode.Nodepool, podNode.Name)

				// Record a critical metric for running incompatible pods
				p.collector.RecordDaemonsetNodepoolMismatch(
					daemonsetName,
					daemonsetNamespace,
					podNode.Name,
					podNode.Nodepool,
				)
			}
		}
	}

	if runningIncompatibleCount > 0 {
		klog.Errorf("🚨 CRITICAL: Found %d pods from incompatible daemonsets running on incompatible nodepool nodes!", runningIncompatibleCount)
		klog.Error("This indicates a serious scheduling issue that needs immediate attention!")
	} else {
		klog.Info("✓ No pods from incompatible daemonsets are running on incompatible nodepool nodes")
	}
}

// checkDaemonsetNodeCompatibility checks if a daemonset is compatible with a specific node's nodepool
func (p *NodePoolDaemonsetProcessor) checkDaemonsetNodeCompatibility(daemonset *appsv1.DaemonSet, daemonsetRequirements scheduling.Requirements, nodeState *informers.NodeInformerState, nodepoolState *informers.NodePoolState) bool {
	if nodepoolState.Requirements == nil {
		klog.V(3).Infof("NodePool %s has no requirements - compatible with any daemonset", nodeState.Nodepool)
		return true
	}

	// Check if the daemonset's requirements are compatible with the NodePool's requirements
	// Use Karpenter's built-in IsCompatible method to check compatibility
	if !nodepoolState.Requirements.IsCompatible(daemonsetRequirements) {
		klog.V(3).Infof("Daemonset %s/%s requirements are not compatible with NodePool %s requirements",
			daemonset.Namespace, daemonset.Name, nodeState.Nodepool)
		return false
	}

	return true
}
