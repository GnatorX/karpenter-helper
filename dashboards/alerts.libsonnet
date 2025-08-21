local utils = import 'mixin-utils/utils.libsonnet';

{
  prometheusAlerts+:: {
    groups+: [
      {
        name: 'karpenter-issues',
        rules: [
          {
            alert: 'KarpenterDaemonsetNodepoolMismatch',
            expr: |||
              increase(karpenter_helper_daemonset_nodepool_mismatch_total[1h]) > %d
            ||| % $._config.alerting.daemonsetMismatchThreshold,
            'for': '5m',
            labels: {
              severity: 'warning',
            },
            annotations: {
              summary: 'Daemonset running on mismatched nodepool',
              description: 'Daemonset {{ $labels.daemonset }} in namespace {{ $labels.namespace }} has {{ $value }} instances running on nodepool {{ $labels.nodepool }} which may not match its scheduling requirements.',
            },
          },
          {
            alert: 'KarpenterHighTTLDeathRate',
            expr: |||
              rate(karpenter_helper_node_ttl_deaths_total[5m]) > %f
            ||| % $._config.alerting.ttlDeathRateThreshold,
            'for': '10m',
            labels: {
              severity: 'info',
            },
            annotations: {
              summary: 'High rate of node TTL deaths',
              description: 'Nodepool {{ $labels.nodepool }} is experiencing {{ $value | humanize }} node deaths per minute due to TTL expiration.',
            },
          },
          {
            alert: 'KarpenterNominatedPodFailures',
            expr: |||
              increase(karpenter_helper_nominated_pod_failures_total[1h]) > %d
            ||| % $._config.alerting.nominationFailureThreshold,
            'for': '5m',
            labels: {
              severity: 'warning',
            },
            annotations: {
              summary: 'High rate of nominated pod failures',
              description: 'Pod {{ $labels.pod }} in namespace {{ $labels.namespace }} has failed to schedule on its nominated node {{ $labels.node }} {{ $value }} times in the last hour. Reason: {{ $labels.reason }}',
            },
          },
          {
            alert: 'KarpenterNodeNotReapedDueToNomination',
            expr: |||
              karpenter_helper_nodes_not_reaped_due_to_nomination > 0
            |||,
            'for': '15m',
            labels: {
              severity: 'warning',
            },
            annotations: {
              summary: 'Node not being reaped due to nominated pod',
              description: 'Node {{ $labels.node }} in nodepool {{ $labels.nodepool }} is not being reaped due to nominated pod {{ $labels.nominated_pod }} in namespace {{ $labels.nominated_namespace }}.',
            },
          },
          {
            alert: 'KarpenterNodeReapingDelayed',
            expr: |||
              histogram_quantile(0.95, rate(karpenter_helper_node_reaping_delay_seconds_bucket[5m])) > %d
            ||| % $._config.alerting.reapingDelayThreshold,
            'for': '10m',
            labels: {
              severity: 'warning',
            },
            annotations: {
              summary: 'Node reaping significantly delayed',
              description: '95th percentile of node reaping delays in nodepool {{ $labels.nodepool }} is {{ $value | humanizeDuration }}, which exceeds the threshold.',
            },
          },
        ],
      },
    ],
  },
}