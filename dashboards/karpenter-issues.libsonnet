local grafana = import 'grafonnet/grafana.libsonnet';
local dashboard = grafana.dashboard;
local template = grafana.template;
local graphPanel = grafana.graphPanel;
local statPanel = grafana.statPanel;
local tablePanel = grafana.tablePanel;

{
  grafanaDashboards+:: {
    'karpenter-issues.json':
      dashboard.new(
        'Karpenter Issues Dashboard',
        time_from='now-1h',
        time_to='now',
        refresh='30s',
        tags=['karpenter', 'kubernetes'],
        uid='karpenter-issues'
      )
      .addTemplate(
        template.datasource(
          'datasource',
          'prometheus',
          'Prometheus',
          hide='label',
        )
      )
      .addTemplate(
        template.query(
          'cluster',
          '$datasource',
          'label_values(karpenter_helper_daemonset_nodepool_mismatch_total, cluster)',
          label='Cluster',
          refresh='load',
          multi=true,
          includeAll=true,
          allValues='.*'
        )
      )
      .addTemplate(
        template.query(
          'nodepool',
          '$datasource', 
          'label_values(karpenter_helper_node_ttl_deaths_total{cluster=~"$cluster"}, nodepool)',
          label='Nodepool',
          refresh='load',
          multi=true,
          includeAll=true,
          allValues='.*'
        )
      )
      
      // Row 1: Overview Stats
      .addPanel(
        statPanel.new(
          'Daemonset Mismatches (24h)',
          datasource='$datasource',
          reducerFunction='lastNotNull',
          colorMode='background'
        )
        .addTarget(
          expr='increase(karpenter_helper_daemonset_nodepool_mismatch_total{cluster=~"$cluster"}[24h])',
          legendFormat='{{daemonset}}/{{namespace}}'
        )
        { gridPos: { h: 4, w: 6, x: 0, y: 0 } }
      )
      .addPanel(
        statPanel.new(
          'TTL Deaths (24h)',
          datasource='$datasource',
          reducerFunction='lastNotNull',
          colorMode='background'
        )
        .addTarget(
          expr='increase(karpenter_helper_node_ttl_deaths_total{cluster=~"$cluster"}[24h])',
          legendFormat='{{nodepool}}'
        )
        { gridPos: { h: 4, w: 6, x: 6, y: 0 } }
      )
      .addPanel(
        statPanel.new(
          'Nominated Pod Failures (24h)',
          datasource='$datasource',
          reducerFunction='lastNotNull',
          colorMode='background'
        )
        .addTarget(
          expr='increase(karpenter_helper_nominated_pod_failures_total{cluster=~"$cluster"}[24h])',
          legendFormat='{{reason}}'
        )
        { gridPos: { h: 4, w: 6, x: 12, y: 0 } }
      )
      .addPanel(
        statPanel.new(
          'Nodes Not Reaped',
          datasource='$datasource',
          reducerFunction='lastNotNull',
          colorMode='background'
        )
        .addTarget(
          expr='sum(karpenter_helper_nodes_not_reaped_due_to_nomination{cluster=~"$cluster"})',
          legendFormat='Current'
        )
        { gridPos: { h: 4, w: 6, x: 18, y: 0 } }
      )
      
      // Row 2: Daemonset Issues
      .addPanel(
        graphPanel.new(
          'Daemonset Nodepool Mismatches Rate',
          datasource='$datasource',
          format='ops',
          legend_rightSide=true,
          legend_alignAsTable=true
        )
        .addTarget(
          expr='rate(karpenter_helper_daemonset_nodepool_mismatch_total{cluster=~"$cluster", nodepool=~"$nodepool"}[5m])',
          legendFormat='{{daemonset}}/{{namespace}} on {{nodepool}}'
        )
        { gridPos: { h: 8, w: 12, x: 0, y: 4 } }
      )
      .addPanel(
        tablePanel.new(
          'Top Daemonset Mismatches',
          datasource='$datasource'
        )
        .addTarget(
          expr='topk(10, increase(karpenter_helper_daemonset_nodepool_mismatch_total{cluster=~"$cluster"}[1h]))',
          format='table',
          instant=true
        )
        .addColumn('Time', 'time')
        .addColumn('Daemonset', 'daemonset')
        .addColumn('Namespace', 'namespace') 
        .addColumn('Nodepool', 'nodepool')
        .addColumn('Count (1h)', 'Value')
        { gridPos: { h: 8, w: 12, x: 12, y: 4 } }
      )
      
      // Row 3: Node TTL Issues
      .addPanel(
        graphPanel.new(
          'Node TTL Deaths by Reason',
          datasource='$datasource',
          format='short',
          stack=true
        )
        .addTarget(
          expr='rate(karpenter_helper_node_ttl_deaths_total{cluster=~"$cluster", nodepool=~"$nodepool"}[5m])',
          legendFormat='{{nodepool}} - {{ttl_reason}}'
        )
        { gridPos: { h: 8, w: 12, x: 0, y: 12 } }
      )
      .addPanel(
        graphPanel.new(
          'Node Reaping Delays',
          datasource='$datasource',
          format='s'
        )
        .addTarget(
          expr='histogram_quantile(0.95, rate(karpenter_helper_node_reaping_delay_seconds_bucket{cluster=~"$cluster", nodepool=~"$nodepool"}[5m]))',
          legendFormat='p95 - {{nodepool}}'
        )
        .addTarget(
          expr='histogram_quantile(0.50, rate(karpenter_helper_node_reaping_delay_seconds_bucket{cluster=~"$cluster", nodepool=~"$nodepool"}[5m]))',
          legendFormat='p50 - {{nodepool}}'
        )
        { gridPos: { h: 8, w: 12, x: 12, y: 12 } }
      )
      
      // Row 4: Nominated Pod Issues  
      .addPanel(
        graphPanel.new(
          'Nominated Pod Failures by Reason',
          datasource='$datasource',
          format='ops',
          stack=true
        )
        .addTarget(
          expr='rate(karpenter_helper_nominated_pod_failures_total{cluster=~"$cluster"}[5m])',
          legendFormat='{{reason}}'
        )
        { gridPos: { h: 8, w: 12, x: 0, y: 20 } }
      )
      .addPanel(
        graphPanel.new(
          'Nodes Created for Nominated Pods',
          datasource='$datasource',
          format='ops'
        )
        .addTarget(
          expr='rate(karpenter_helper_nodes_created_for_nominated_pods_total{cluster=~"$cluster", nodepool=~"$nodepool"}[5m])',
          legendFormat='{{nodepool}}'
        )
        { gridPos: { h: 8, w: 12, x: 12, y: 20 } }
      )
      
      // Row 5: Node Reaping Issues Detail
      .addPanel(
        tablePanel.new(
          'Nodes Not Being Reaped',
          datasource='$datasource'
        )
        .addTarget(
          expr='karpenter_helper_nodes_not_reaped_due_to_nomination{cluster=~"$cluster", nodepool=~"$nodepool"} > 0',
          format='table',
          instant=true
        )
        .addColumn('Node', 'node')
        .addColumn('Nodepool', 'nodepool')
        .addColumn('Nominated Pod', 'nominated_pod')
        .addColumn('Pod Namespace', 'nominated_namespace')
        { gridPos: { h: 8, w: 24, x: 0, y: 28 } }
      ),
  },
}