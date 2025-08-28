# Karpenter Helper

A Kubernetes operator that monitors and emits metrics for common Karpenter issues to help with observability and debugging.

## Issues Detected

This tool monitors and provides metrics for the following Karpenter issues:

1. **Daemonset Nodepool Mismatches**: Daemonsets scheduled on nodes that don't match their nodepool selectors
2. **Node TTL Deaths**: Nodes deleted due to TTL expiration  
3. **Nominated Pod Failures**: Nodes provisioned for specific pods that never get scheduled on them
4. **Node Reaping Issues**: Nodes that should be cleaned up but remain due to pending nominations

## Metrics Exposed (not fully implemented yet)

| Metric | Type | Description | Labels |
|--------|------|-------------|--------|
| `karpenter_helper_daemonset_nodepool_mismatch_total` | Counter | Daemonset pods on mismatched nodepools | `daemonset`, `namespace`, `node`, `nodepool` |
| `karpenter_helper_node_ttl_deaths_total` | Counter | Nodes deleted due to TTL | `node`, `nodepool`, `ttl_reason` |
| `karpenter_helper_nominated_pod_failures_total` | Counter | Nominated pods that failed to land | `pod`, `namespace`, `node`, `reason` |
| `karpenter_helper_nodes_created_for_nominated_pods_total` | Counter | Nodes created for nominations | `node`, `pod`, `namespace`, `nodepool` |
| `karpenter_helper_nodes_not_reaped_due_to_nomination` | Gauge | Nodes prevented from reaping by nominations | `node`, `nodepool`, `nominated_pod`, `nominated_namespace` |
| `karpenter_helper_node_reaping_delay_seconds` | Histogram | Delay in node reaping | `node`, `nodepool`, `delay_reason` |

## Installation

### Prerequisites

- Go 1.24
- Kubernetes cluster with Karpenter installed
- Prometheus for metrics collection

### Building

```bash
go mod tidy
go build -o karpenter-helper main.go
```

### Running

```bash
# In-cluster
./karpenter-helper

# With kubeconfig
./karpenter-helper -kubeconfig ~/.kube/config

# Custom metrics port
./karpenter-helper -metrics-addr :9090
```

### Kubernetes Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: karpenter-helper
  namespace: karpenter
spec:
  replicas: 1
  selector:
    matchLabels:
      app: karpenter-helper
  template:
    metadata:
      labels:
        app: karpenter-helper
    spec:
      serviceAccountName: karpenter-helper
      containers:
      - name: karpenter-helper
        image: karpenter-helper:latest
        ports:
        - containerPort: 8080
          name: metrics
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 512Mi
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: karpenter-helper
  namespace: karpenter
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: karpenter-helper
rules:
- apiGroups: [""]
  resources: ["nodes", "pods"]
  verbs: ["get", "list", "watch"]
- apiGroups: ["apps"]
  resources: ["daemonsets"]
  verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: karpenter-helper
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: karpenter-helper
subjects:
- kind: ServiceAccount
  name: karpenter-helper
  namespace: karpenter
---
apiVersion: v1
kind: Service
metadata:
  name: karpenter-helper-metrics
  namespace: karpenter
  labels:
    app: karpenter-helper
spec:
  selector:
    app: karpenter-helper
  ports:
  - name: metrics
    port: 8080
    targetPort: 8080
```

## Dashboards

The project includes Grafana dashboards built with kubernetes-mixin:

### Setup

```bash
cd dashboards
# Install jsonnet dependencies
jb install
# Generate dashboards
jsonnet -J vendor -m . mixin.libsonnet
```

The generated `karpenter-issues.json` can be imported into Grafana.

### Dashboard Features

- Overview stats for all issue types
- Time-series graphs for issue rates
- Detailed tables of current problems
- Configurable cluster and nodepool filtering

## Configuration

The helper is configured via command-line flags:

- `-kubeconfig`: Path to kubeconfig (optional, uses in-cluster config by default)
- `-metrics-addr`: Address to serve metrics (default: `:8080`)

## Development

### Running Tests

```bash
go test ./...
```

### Adding New Issue Detection

1. Add metrics to `pkg/metrics/collector.go`
2. Implement detection logic in appropriate watcher
3. Update dashboard queries in `dashboards/`
4. Add alerts in `dashboards/alerts.libsonnet`

## Alerts

The project includes Prometheus alert rules for:

- High daemonset mismatch rates
- Excessive TTL death rates  
- Nomination failure spikes
- Nodes stuck due to nominations
- Significant reaping delays

Import `alerts.libsonnet` into your Prometheus configuration.

# Logging

This project will log when it finds issues 



## Incompatible Daemonset pod running on incompatible nodepool nodes
```
I0828 11:21:08.007042   45566 nodepool_daemonset_processor.go:137] Checking for pods from incompatible daemonsets running on incompatible nodepool nodes
W0828 11:21:08.007307   45566 nodepool_daemonset_processor.go:215] 🚨 CRITICAL: Pod aws-k8s-system/aws-node-binpacking-v2-wg62s from incompatible daemonset aws-k8s-system/aws-node-binpacking-v2 is running on incompatible nodepool qa-northwest-dev-shared (node: i-instance-id.worker)
E0828 11:21:08.007693   45566 nodepool_daemonset_processor.go:230] 🚨 CRITICAL: Found 1 pods from incompatible daemonsets running on incompatible nodepool nodes!
E0828 11:21:08.007702   45566 nodepool_daemonset_processor.go:231] This indicates misconfiguration in nodepool
```