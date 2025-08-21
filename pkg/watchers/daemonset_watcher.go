package watchers

import (
	"context"
	"sync"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	klog "k8s.io/klog/v2"

	"github.com/garvinp/karpenter-helper/pkg/metrics"
)

type DaemonsetWatcher struct {
	clientset *kubernetes.Clientset
	collector *metrics.Collector

	mu         sync.RWMutex
	daemonsets map[string]*appsv1.DaemonSet // key: namespace/name
}

func NewDaemonsetWatcher(clientset *kubernetes.Clientset, collector *metrics.Collector) (*DaemonsetWatcher, error) {
	return &DaemonsetWatcher{
		clientset:  clientset,
		collector:  collector,
		daemonsets: make(map[string]*appsv1.DaemonSet),
	}, nil
}

func (dw *DaemonsetWatcher) Run(ctx context.Context) {
	klog.Info("Starting daemonset watcher")

	watchlist := &metav1.ListOptions{
		FieldSelector: fields.Everything().String(),
	}

	watcher, err := dw.clientset.AppsV1().DaemonSets("").Watch(ctx, *watchlist)
	if err != nil {
		klog.Errorf("Failed to create daemonset watcher: %v", err)
		return
	}
	defer watcher.Stop()

	for event := range watcher.ResultChan() {
		switch event.Type {
		case watch.Added:
			dw.handleDaemonsetAdded(event.Object.(*appsv1.DaemonSet))
		case watch.Modified:
			dw.handleDaemonsetModified(event.Object.(*appsv1.DaemonSet))
		case watch.Deleted:
			dw.handleDaemonsetDeleted(event.Object.(*appsv1.DaemonSet))
		}
	}
}

func (dw *DaemonsetWatcher) handleDaemonsetAdded(ds *appsv1.DaemonSet) {
	dw.mu.Lock()
	defer dw.mu.Unlock()

	dsKey := ds.Namespace + "/" + ds.Name
	dw.daemonsets[dsKey] = ds

	klog.V(2).Infof("Added daemonset %s", dsKey)
}

func (dw *DaemonsetWatcher) handleDaemonsetModified(ds *appsv1.DaemonSet) {
	dw.mu.Lock()
	defer dw.mu.Unlock()

	dsKey := ds.Namespace + "/" + ds.Name
	dw.daemonsets[dsKey] = ds
}

func (dw *DaemonsetWatcher) handleDaemonsetDeleted(ds *appsv1.DaemonSet) {
	dw.mu.Lock()
	defer dw.mu.Unlock()

	dsKey := ds.Namespace + "/" + ds.Name
	delete(dw.daemonsets, dsKey)

	klog.V(2).Infof("Deleted daemonset %s", dsKey)
}

// CheckDaemonsetNodepoolMismatches checks for daemonset pods running on nodes that don't match nodepool requirements
func (dw *DaemonsetWatcher) CheckDaemonsetNodepoolMismatches() {
	dw.mu.RLock()
	daemonsets := make(map[string]*appsv1.DaemonSet)
	for k, v := range dw.daemonsets {
		daemonsets[k] = v
	}
	dw.mu.RUnlock()

	// Get all nodes
	nodes, err := dw.clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		klog.Errorf("Failed to list nodes: %v", err)
		return
	}

	for _, ds := range daemonsets {
		dw.checkDaemonsetOnNodes(ds, nodes.Items)
	}
}

func (dw *DaemonsetWatcher) checkDaemonsetOnNodes(ds *appsv1.DaemonSet, nodes []corev1.Node) {
	// Get daemonset pods
	selector, err := metav1.LabelSelectorAsSelector(ds.Spec.Selector)
	if err != nil {
		klog.Errorf("Invalid selector for daemonset %s/%s: %v", ds.Namespace, ds.Name, err)
		return
	}

	pods, err := dw.clientset.CoreV1().Pods(ds.Namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: selector.String(),
	})
	if err != nil {
		klog.Errorf("Failed to list pods for daemonset %s/%s: %v", ds.Namespace, ds.Name, err)
		return
	}

	// Check each pod against its node's nodepool compatibility
	for _, pod := range pods.Items {
		if pod.Spec.NodeName == "" {
			continue // Pod not scheduled yet
		}

		// Find the node this pod is running on
		var podNode *corev1.Node
		for _, node := range nodes {
			if node.Name == pod.Spec.NodeName {
				podNode = &node
				break
			}
		}

		if podNode == nil {
			continue // Node not found
		}

		// Check if daemonset should run on this node based on nodepool
		if dw.shouldDaemonsetRunOnNode(ds, podNode) {
			// This is expected - daemonset should run here
			continue
		}

		// Daemonset is running on a node where it shouldn't based on nodepool constraints
		nodepool := podNode.Labels[KarpenterNodePoolLabel]
		if nodepool == "" {
			nodepool = podNode.Labels[KarpenterProvisionerLabel]
		}

		if nodepool != "" {
			dw.collector.RecordDaemonsetNodepoolMismatch(ds.Name, ds.Namespace, podNode.Name, nodepool)
			klog.V(2).Infof("Daemonset %s/%s running on node %s with mismatched nodepool %s",
				ds.Namespace, ds.Name, podNode.Name, nodepool)
		}
	}
}

func (dw *DaemonsetWatcher) shouldDaemonsetRunOnNode(ds *appsv1.DaemonSet, node *corev1.Node) bool {
	// Check node selector
	if ds.Spec.Template.Spec.NodeSelector != nil {
		nodeLabels := labels.Set(node.Labels)
		selector := labels.Set(ds.Spec.Template.Spec.NodeSelector)
		if !selector.AsSelector().Matches(nodeLabels) {
			return false
		}
	}

	// Check node affinity
	if ds.Spec.Template.Spec.Affinity != nil && ds.Spec.Template.Spec.Affinity.NodeAffinity != nil {
		nodeAffinity := ds.Spec.Template.Spec.Affinity.NodeAffinity

		// Check required terms
		if nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil {
			terms := nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
			if !dw.nodeMatchesAnyTerm(node, terms) {
				return false
			}
		}
	}

	// Check tolerations vs taints
	if !dw.podToleratesNodeTaints(ds.Spec.Template.Spec.Tolerations, node.Spec.Taints) {
		return false
	}

	return true
}

func (dw *DaemonsetWatcher) nodeMatchesAnyTerm(node *corev1.Node, terms []corev1.NodeSelectorTerm) bool {
	for _, term := range terms {
		if dw.nodeMatchesTerm(node, term) {
			return true
		}
	}
	return len(terms) == 0
}

func (dw *DaemonsetWatcher) nodeMatchesTerm(node *corev1.Node, term corev1.NodeSelectorTerm) bool {
	for _, req := range term.MatchExpressions {
		if !dw.nodeMatchesExpression(node, req) {
			return false
		}
	}
	for _, req := range term.MatchFields {
		if !dw.nodeMatchesFieldExpression(node, req) {
			return false
		}
	}
	return true
}

func (dw *DaemonsetWatcher) nodeMatchesExpression(node *corev1.Node, req corev1.NodeSelectorRequirement) bool {
	nodeValue, exists := node.Labels[req.Key]

	switch req.Operator {
	case corev1.NodeSelectorOpIn:
		return exists && dw.stringInSlice(nodeValue, req.Values)
	case corev1.NodeSelectorOpNotIn:
		return !exists || !dw.stringInSlice(nodeValue, req.Values)
	case corev1.NodeSelectorOpExists:
		return exists
	case corev1.NodeSelectorOpDoesNotExist:
		return !exists
	case corev1.NodeSelectorOpGt, corev1.NodeSelectorOpLt:
		// Numeric comparisons - simplified for this example
		return exists
	}
	return false
}

func (dw *DaemonsetWatcher) nodeMatchesFieldExpression(node *corev1.Node, req corev1.NodeSelectorRequirement) bool {
	// Simplified field matching - would need full implementation for production
	return true
}

func (dw *DaemonsetWatcher) podToleratesNodeTaints(tolerations []corev1.Toleration, taints []corev1.Taint) bool {
	for _, taint := range taints {
		if taint.Effect == corev1.TaintEffectNoSchedule || taint.Effect == corev1.TaintEffectNoExecute {
			tolerated := false
			for _, toleration := range tolerations {
				if dw.tolerationMatches(toleration, taint) {
					tolerated = true
					break
				}
			}
			if !tolerated {
				return false
			}
		}
	}
	return true
}

func (dw *DaemonsetWatcher) tolerationMatches(toleration corev1.Toleration, taint corev1.Taint) bool {
	if toleration.Key == "" {
		return toleration.Operator == corev1.TolerationOpExists
	}

	if toleration.Key != taint.Key {
		return false
	}

	if toleration.Operator == corev1.TolerationOpExists {
		return true
	}

	return toleration.Value == taint.Value && (toleration.Effect == "" || toleration.Effect == taint.Effect)
}

func (dw *DaemonsetWatcher) stringInSlice(str string, slice []string) bool {
	for _, s := range slice {
		if s == str {
			return true
		}
	}
	return false
}
