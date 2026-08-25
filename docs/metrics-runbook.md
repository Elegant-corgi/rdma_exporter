# RDMA Exporter Metrics Runbook (RoCEv2 + mlx5)

This document is an operator-facing "metrics dictionary" for `rdma_exporter`, focused on RoCEv2 fabrics built on mlx5 adapters. It classifies every counter family by operational severity, explains how to read it correctly, and lays out alerting and dashboarding guidance derived from field experience.

Linux exposes the standard RDMA counters under `/sys/class/infiniband/<dev>/<port>/counters`, and mlx5 adds hardware counters under `hw_counters` in the same port directory. `rdma_exporter` walks both directories dynamically, so counters added later by the driver or kernel can surface automatically as `rdma_<name>_total` even if this document does not mention them by name yet. Treat any metric whose name is not covered below as an unclassified, informational series until it is triaged.

## 1. Severity tiers

Every metric below is tagged with one of four tiers:

- **A — Fault indicators.** Should not increase under normal operation. Primary alert candidates.
- **B — Congestion / performance degradation.** Increases can be perfectly normal; what matters is sustained or bursty growth, not any single non-zero sample.
- **C — Utilization.** Increasing is the expected, healthy behavior. Useful for dashboards and for normalizing error rates against traffic volume.
- **D — Metadata / exporter health.** Preconditions for trusting the rest of the metrics; not workload signals themselves.

## 2. Port inventory

| Metric | Healthy value | Tier | Alert | Dashboard | Primary layer |
|---|---|---:|---:|---:|---|
| `rdma_port_info` | `state="ACTIVE"`, `phys_state="LINK_UP"` | A/D | ✅ | ✅ | Link |
| `rdma_lifespan_milliseconds` | Stable at the configured value | D | Optional | Optional | Telemetry |

`rdma_port_info` is the most important inventory metric the exporter emits. It carries `device`, `netdev`, `pci_addr`, and PF/VF relationship labels, which makes it the natural join key for correlating RDMA state with other NIC, PCIe, and host telemetry sources. Those labels are defined explicitly in the collector, so they are stable across scrapes.

A minimal health check is:

```promql
rdma_port_info{state!="ACTIVE"}
```

However, don't wire this directly into an alert without an inventory layer that defines which ports are *supposed* to be active. Unused ports and intentionally administratively-down ports will otherwise generate noise.

## 3. Traffic counters

| Metric | Healthy behavior | Tier | Alert | Dashboard |
|---|---|---:|---:|---:|
| `rdma_port_rcv_data_total` | Increases with workload | C | ❌ | ✅ |
| `rdma_port_xmit_data_total` | Increases with workload | C | ❌ | ✅ |
| `rdma_port_rcv_packets_total` | Increases with workload | C | ❌ | ✅ |
| `rdma_port_xmit_packets_total` | Increases with workload | C | ❌ | ✅ |
| `rdma_unicast_rcv_packets_total` | Increases with workload | C | ❌ | Optional |
| `rdma_unicast_xmit_packets_total` | Increases with workload | C | ❌ | Optional |
| `rdma_multicast_rcv_packets_total` | Workload-dependent | C | ❌ | Optional |
| `rdma_multicast_xmit_packets_total` | Workload-dependent | C | ❌ | Optional |

`port_*_data` counters are reported in 4-byte words (doublewords), not bytes, per the Linux RDMA ABI ("data octets divided by 4"). Convert accordingly:

```promql
# Bytes/s
4 * rate(rdma_port_rcv_data_total[1m])

# Gbit/s
32 * rate(rdma_port_rcv_data_total[1m]) / 1e9
```

Forgetting this factor of 4 is one of the most common mistakes when building an `rdma_exporter` dashboard from scratch — double-check any panel that reports raw byte/bit rates.

## 4. Errors that should be zero

| Metric | Healthy | Meaning of an increase | Tier |
|---|---|---|---:|
| `rdma_port_rcv_errors_total` | No growth | RX errors | A |
| `rdma_symbol_error_total` | Ideally zero | Physical-lane symbol errors | A |
| `rdma_local_link_integrity_errors_total` | Zero | PHY error threshold exceeded | A |
| `rdma_excessive_buffer_overrun_errors_total` | Zero | Buffer, hardware, or configuration problem | A |
| `rdma_link_downed_total` | Zero | Link recovery failed and the link went down | A |
| `rdma_port_xmit_discards_total` | Zero | TX drops from congestion or a downed link | A |
| `rdma_port_rcv_remote_physical_errors_total` | Zero | Remote physical error | A / IB |
| `rdma_port_rcv_constraint_errors_total` | Zero | Constraint violation | A / IB |
| `rdma_port_xmit_constraint_errors_total` | Zero | Constraint violation | A / IB |
| `rdma_port_rcv_switch_relay_errors_total` | Zero | Switch relay discard | A / IB |
| `rdma_vl15_dropped_total` | Zero | Management VL buffer exhaustion | A / IB |

Within this group, the metrics that matter most on a RoCE host are:

```text
port_rcv_errors
link_downed
port_xmit_discards
symbol_error
local_link_integrity_errors
```

`vl15_dropped` and the constraint-violation counters are strongly InfiniBand-specific. On a RoCE-only cluster, they can remain in the metric set (the exporter still publishes them if the kernel reports them) but don't need a spot on the top-level dashboard.

Linux defines `port_xmit_discards` as outbound discards caused by a downed port or by congestion, and `link_downed` as the count of times link-error recovery failed and the port transitioned down.

## 5. `link_error_recovery` is not, by itself, an outage

```text
rdma_link_error_recovery_total
```

counts the number of times the link training state machine recovered *after* a problem occurred — recovery, not failure.

Read it together with `link_downed`:

```text
link_error_recovery ↑
link_downed = 0
```

→ self-healed link instability. Worth tracking, not necessarily paging.

```text
link_error_recovery ↑
link_downed ↑
```

→ a more serious physical link problem; recovery is no longer keeping up.

In practice, plot both on the same panel:

```promql
increase(rdma_link_error_recovery_total[15m])
```

```promql
increase(rdma_link_downed_total[15m])
```

## 6. RDMA transport error priority

This tier is especially important when investigating RoCE job failures.

| Metric | Healthy | Meaning | Likely cause | Priority |
|---|---|---|---|---:|
| `local_ack_timeout_err` | Ideally zero | ACK timeout | Loss, congestion, or delay | A |
| `packet_seq_err` | Zero | Sequence NAK | Packet loss / reorder | A |
| `out_of_sequence` | Zero | Out-of-sequence packet | Loss / reorder | A |
| `implied_nak_seq_err` | Zero | PSN mismatch against expected | Loss / reorder | A |
| `duplicate_request` | Ideally zero | Duplicate request | Retransmission | B/A |
| `rnr_nak_retry_err` | Ideally zero | Receiver Not Ready | Receiver / application | B/A |
| `out_of_buffer` | Zero | Insufficient QP receive WQEs | Receiver / application | A |

NVIDIA documents `local_ack_timeout_err` as the RC/XRC/DCT sender-side ACK timer expiring.

### The typical network-loss signature

```text
packet_seq_err ↑
implied_nak_seq_err ↑
duplicate_request ↑
local_ack_timeout_err ↑
```

rising together is a strong indicator of:

```text
RDMA transport
     ↑
packet loss / reordering / congestion
```

When you see this pattern, immediately pivot to:

```text
PFC
ECN / CNP
PHY / FEC
switch drops
```

## 7. Separate RNR / `out_of_buffer` from network congestion

The following combination deserves its own diagnosis path:

```text
rdma_rnr_nak_retry_err_total ↑
rdma_out_of_buffer_total ↑
```

This is usually not switch congestion. It points toward:

```text
receiver application
       ↓
not posting enough receive WQEs
       ↓
receiver not ready / WQE shortage
```

If network-side counters (PFC, ECN, PHY) are clean but RNR alone climbs — a common pattern with NCCL and similar collective libraries — investigate:

- application / QP behavior
- receiver-side scheduling
- CPU starvation
- verbs queue management (posted WQE depth)

## 8. CQE / verbs errors

These belong to the "should be zero" group as well.

| Metric | Meaning | Primary layer |
|---|---|---|
| `rdma_req_cqe_error_total` | Requester CQE error | RDMA / application |
| `rdma_resp_cqe_error_total` | Responder CQE error | RDMA / application |
| `rdma_req_cqe_flush_error_total` | Requester WR flush | Secondary QP failure |
| `rdma_resp_cqe_flush_error_total` | Responder WR flush | Secondary QP failure |
| `rdma_req_remote_access_errors_total` | Remote access error | MR / rkey |
| `rdma_resp_remote_access_errors_total` | Remote access error | MR / rkey |
| `rdma_req_remote_invalid_request_total` | Invalid request | verbs / application |
| `rdma_resp_local_length_error_total` | Local buffer length mismatch | Application |

Flush errors need extra care during incident analysis. If the failure sequence is:

```text
remote access error
       ↓
QP → ERROR state
       ↓
outstanding WRs get flushed
       ↓
a burst of cqe_flush_error
```

then `cqe_flush_error`, even though it has the highest count, is *not* the root cause — it's a downstream symptom. When triaging, prefer looking at which metric's `increase(...[1m])` rose *first*, rather than which one has the largest magnitude.

## 9. Reading ECN / CNP

For RoCEv2, read these four counters as one set:

```text
rdma_np_ecn_marked_roce_packets_total
rdma_np_cnp_sent_total

rdma_rp_cnp_handled_total
rdma_rp_cnp_ignored_total
```

The feedback loop they represent:

```text
      congested switch
             │
             │ ECN CE
             ▼
 Receiver / NP
 np_ecn_marked_roce_packets
             │
             │ CNP
             ▼
 Sender / RP
       rp_cnp_handled
             │
             ▼
       transmission rate ↓
```

### `np_ecn_marked_roce_packets`

**An increase is not, by itself, a fault.** If ECN is working as designed, this metric rising during congestion is expected behavior.

Tier: **B**.

### `np_cnp_sent`

Also expected to rise as part of a healthy feedback loop.

Tier: **B**.

### `rp_cnp_handled`

Counts CNPs received and acted on by throttling the send rate. This means congestion control *worked* — do not alert on it alone.

Tier: **B**.

### `rp_cnp_ignored`

This one is different. Vendor documentation for these counters states that on a network with RoCE congestion control enabled, this counter should not increase; a rising value should prompt a check of the adapter's ECN configuration.

That makes this a solid configuration-drift alert candidate:

```promql
increase(rdma_rp_cnp_ignored_total[5m]) > 0
```

## 10. PFC: watch occupancy, not just event count

PFC surfaces through three counter families:

```text
rdma_roce_pfc_pause_frames_total
rdma_roce_pfc_pause_duration_total
rdma_roce_pfc_pause_transitions_total
```

The most useful of the three is duration. The counter is in microseconds, so:

```promql
rate(
  rdma_roce_pfc_pause_duration_total[1m]
) / 1e6
```

gives pause **occupancy** as a fraction of wall-clock time:

```text
0.00 → 0%
0.01 → 1%
0.10 → 10%
0.50 → 50%
```

### Frame counts alone aren't enough

Compare:

```text
10,000 very short pauses
```

with:

```text
10 pauses of 100 ms each
```

Both could report similar frame counts, but their performance impact is completely different — one barely registers, the other stalls the link for a full second.

Recommended dashboard priority:

1. Pause duration / occupancy
2. Transitions
3. Frame count

## 11. Use PFC direction to localize the cause

This is one of the most actionable signals in the whole metric set.

### `direction="rx"`

The NIC *received* a pause frame:

```text
HOST/NIC ──────────> SWITCH / NETWORK
                    ↑
                downstream pressure

         <──── XOFF
```

The peer is telling this NIC to stop transmitting. Investigate in this order:

```text
switch buffers
downstream link
receiver side
fabric hotspot
```

### `direction="tx"`

The NIC *sent* a pause frame:

```text
peer ──────────────> HOST/NIC
                         │
                cannot drain RX fast enough
                         │
         XOFF ───────────┘
```

Here the host is applying backpressure to the peer. Investigate:

```text
NIC RX buffers
PCIe
host
receiver-side processing
```

Do not read "PFC went up" as "the switch is congested" without checking direction first.

## 12. Correlate with host/NIC buffer pressure

When `direction="tx"` PFC rises, check the following in the same window:

```text
rdma_netdev_prio_buf_discard_total
rdma_netdev_prio_cong_discard_total
rdma_netdev_prio_discards_total
rdma_netdev_dev_out_of_buffer_total
rdma_netdev_rx_out_of_buffer_total
rdma_netdev_rx_discards_phy_total
```

The collector extracts these directly from mlx5 ethtool stats.

### Interpretation

```text
PFC TX ↑
rx_out_of_buffer ↑
rx_discards_phy ↑
```

strongly suggests the host/NIC cannot absorb incoming traffic fast enough.

Conversely:

```text
PFC RX ↑
host buffer counters = 0
```

points away from the host and toward the fabric or downstream link.

## 13. Combine ECN and PFC

Cross-tabulating PFC and ECN/CNP gives a fairly reliable classification of fabric state.

| PFC | ECN/CNP | Interpretation |
|---|---|---|
| Low | Low | Healthy / low load |
| Low | High | ECN may be controlling congestion early, before PFC needs to engage |
| High | High | Strong congestion |
| High | Low | Check ECN threshold / configuration |
| High TX | Low | Also suspect a host/NIC RX bottleneck |

In particular, if you observe this pattern persisting:

```text
PFC ↑↑
ECN CE ≈ 0
CNP ≈ 0
```

the lossless fabric is pausing, but ECN feedback is barely engaging. This is worth cross-checking against switch-side ECN/PFC threshold configuration.

## 14. Optional `cc_*` counters give additional ECN/CNP visibility

```text
rdma_cc_rx_ce_pkts_total
rdma_cc_rx_cnp_pkts_total
rdma_cc_tx_cnp_pkts_total
```

These come from a separate code path than the always-on port `hw_counters` `np_*`/`rp_*` family (they arrive over RDMA netlink, not sysfs).

Tier: **B — congestion telemetry**, same as the ECN/CNP metrics above.

They are more useful layered on top of PFC than as a standalone alert:

```promql
rate(rdma_cc_rx_ce_pkts_total[1m])
rate(rdma_cc_rx_cnp_pkts_total[1m])
rate(rdma_cc_tx_cnp_pkts_total[1m])
```

These optional counters are disabled in the driver by default, and the exporter never enables them itself. `--collector.optional-counters` is on by default in the exporter, but reading non-zero values for `cc_*` still requires an operator to run `rdma statistic set` to enable the hardware counter, and that enablement does **not** survive a reboot or driver reload.

Enabling optional counters also allocates mlx5 flow-steering rules and flow counters, which can affect datapath performance. Measure the impact before turning them on fleet-wide.

## 15. PHY/FEC: zero isn't the only thing to check

At the physical layer:

```text
rdma_phy_rx_corrected_bits_total
rdma_phy_rx_pcs_symbol_err_total
rdma_phy_rx_bits_total
rdma_phy_rx_err_lane_total
rdma_phy_rx_crc_errors_total
rdma_phy_link_down_events_total
```

### Corrected bits

```text
rdma_phy_rx_corrected_bits_total
```

counts bits that FEC corrected. Strictly speaking:

> non-zero ≠ fault

High-speed Ethernet links are designed on the assumption that FEC will correct some baseline amount of pre-FEC bit errors. So look at the ratio, not the raw value:

```promql
increase(rdma_phy_rx_corrected_bits_total[5m])
/
clamp_min(
  increase(rdma_phy_rx_bits_total[5m]),
  1
)
```

### PCS symbol errors

```text
rdma_phy_rx_pcs_symbol_err_total
```

is a much stronger signal than corrected bits. Even if corrected bits are climbing somewhat, if:

```text
PCS errors = 0
CRC errors = 0
```

then FEC is likely doing its job successfully. But once you also see:

```text
corrected bits ↑↑
PCS symbol error ↑
CRC error ↑
```

physical link quality should be considered suspect.

### Per-lane errors

```text
rdma_phy_rx_err_lane_total{lane="0"}
rdma_phy_rx_err_lane_total{lane="1"}
...
```

is especially valuable for isolating the fault. For example, for a 4-lane link:

```text
lane 0    100
lane 1     80
lane 2  90000
lane 3    110
```

the aggregate BER hides the real signal: lane 2 is clearly, distinctly bad. At this point, suspect:

- transceiver
- fiber / cable
- connector
- SerDes lane

## 16. PHY fault severity progression

In practice, treat these as an escalating severity ladder:

```text
raw lane errors
       ↓
FEC corrected bits
       ↓
FEC/PCS uncorrectable errors
       ↓
CRC/FCS errors
       ↓
link error recovery
       ↓
link down
```

Use the top of the ladder as early warning:

```text
rx_err_lane
corrected_bits
```

and treat the bottom as strong fault signals:

```text
pcs_symbol_err
crc_errors
link_down_events
```

## 17. Always separate PCIe from network

To avoid mislabeling a host-side bottleneck as a "network" problem, watch:

```text
rdma_pcie_outbound_stalled_percent
rdma_pcie_outbound_stalled_seconds_total
rdma_pcie_outbound_buffer_overflow_total
rdma_pcie_signal_integrity_total
```

### `stalled_percent`

A gauge sampled over the last one second — good for dashboards, not for alerting.

```text
0%   good
30%  a significant fraction of the last second was stalled
100% stalled for the entire sample
```

Because this is a 1-second sample, a 15-second scrape interval can miss short stalls entirely between samples. The exporter's own metric help text calls this out explicitly.

### `stalled_seconds_total`

Prefer this counter for alerting:

```promql
rate(rdma_pcie_outbound_stalled_seconds_total[5m])
```

It's a cumulative counter of seconds during which outbound PCIe stall exceeded a 30% threshold. For example:

```text
rate = 0.0
```

means essentially no stall.

```text
rate = 0.2
```

means roughly 20% of the seconds in the window exceeded the threshold.

### PCIe buffer overflow

```text
rdma_pcie_outbound_buffer_overflow_total
```

is more severe, and should be treated as **zero growth expected** under normal operation.

If you see all three together:

```text
PCIe stall ↑
PCIe overflow ↑
PFC TX ↑
```

that's a strong pattern indicating the host/PCIe path — not the network — cannot keep up with NIC traffic and is generating backpressure.

## 18. PCIe signal integrity is a different physical fault class

```text
rdma_pcie_signal_integrity_total{direction="rx|tx"}
```

rising points away from the Ethernet side:

```text
Ethernet cable
optical transceiver
switch
```

and toward the host/PCIe side:

```text
NIC
PCIe slot
riser
motherboard
PCIe link
```

This gives a clean split for triage:

```text
PHY rx errors      → outside the NIC (fabric/cable)
PCIe signal error  → inside the NIC / host side
```

## 19. Pause storm is a strong alert candidate

```text
rdma_netdev_pause_storm_events_total{
  severity="warning|error"
}
```

The collector defines these severities as:

- `warning`: pause TX stayed above a watermark for a sustained period.
- `error`: pause TX timed out and was disabled by the driver — drops may have occurred while pause TX was suppressed.

`error` is a good candidate for a hard alert:

```promql
increase(
  rdma_netdev_pause_storm_events_total{
    severity="error"
  }[5m]
) > 0
```

Treat `warning` as a warning-level signal and `error` as critical.

## 20. Adaptive retransmission

| Metric | Interpretation |
|---|---|
| `rdma_roce_adp_retrans_total` | Recovery mechanism engaging — Tier B |
| `rdma_roce_adp_retrans_to_total` | Timeout — leans Tier A |
| `rdma_roce_slow_restart_total` | Tier B |
| `rdma_roce_slow_restart_cnps_total` | Tier B |
| `rdma_roce_slow_restart_trans_total` | Tier B |

`adaptive retransmission` rising alone means the recovery mechanism engaged in response to loss — a normal response.

`adaptive retransmission timeout` rising means recovery couldn't fully absorb the loss; treat this as one severity tier higher.

## 21. Workload counters are informational, not alertable

```text
rdma_rx_write_requests_total
rdma_rx_read_requests_total
rdma_rx_atomic_requests_total
rdma_rx_dct_connect_total
```

are Tier **C** — expected to grow with workload. Their main use is as a normalizer for error rates:

```promql
rate(rdma_local_ack_timeout_err_total[5m])
/
clamp_min(
  rate(rdma_rx_read_requests_total[5m]),
  1
)
```

This kind of ratio removes the effect of "error count went up because traffic volume went up." That said, sender/receiver scope and operation scope differ per counter, so align counter semantics carefully before treating a ratio like this as a true error probability.

## 22. Treat QP metrics as a drill-down tool

The QP collector (`--collector.qp-counters`) is disabled by default. Per the project's README, this is because a netlink dump of live QP state can exceed the 5-second scrape timeout on dense hosts.

When enabled, it can export at least the following, each labeled with `qp_type`:

```text
rdma_qp_duplicate_request_total
rdma_qp_implied_nak_seq_err_total
rdma_qp_local_ack_timeout_err_total
rdma_qp_packet_seq_err_total
rdma_qp_rnr_nak_retry_err_total
rdma_qp_out_of_buffer_total

rdma_qp_rx_write_requests_total
rdma_qp_rx_read_requests_total
rdma_qp_rx_atomic_requests_total

rdma_qp_rx_bytes_total
rdma_qp_tx_bytes_total
rdma_qp_rx_packets_total
rdma_qp_tx_packets_total
```

For example:

```promql
sum by (instance, device, qp_type) (
  rate(rdma_qp_local_ack_timeout_err_total[1m])
)
```

can isolate whether timeouts are concentrated on RC QPs while UD stays clean.

QP dump values only reflect the set of QPs currently bound to the live auto-type stats set — this is a different scope from the port-level sysfs counters, which include the default pool plus every running set plus history. **Do not sum QP-level and port-level counters** — they are not partitions of one another, and the project's README calls this out explicitly.

## 23. Always monitor exporter health alongside RDMA health

These five counters answer the question "is the observation pipeline itself broken?":

```text
rdma_scrape_errors_total
rdma_roce_pfc_scrape_errors_total
rdma_netdev_scrape_errors_total
rdma_optional_counter_scrape_errors_total
rdma_qp_scrape_errors_total
```

and the collector-level health gauge:

```text
rdma_scrape_collector_success{collector="ethtool"}
rdma_scrape_collector_success{collector="optional-counters"}
rdma_scrape_collector_success{collector="qp-counters"}
```

If a collector is disabled, its `rdma_scrape_collector_success` series simply doesn't exist. If it's enabled but failed to initialize or prepare, the value is `0`.

This distinction matters a lot in practice:

```text
PFC = 0
```

and

```text
the PFC collector is broken
```

look identical if you only watch the PFC metrics themselves. A meta-monitoring rule closes that gap:

```promql
rdma_scrape_collector_success{collector="ethtool"} != 1
```

## 24. A three-tier alerting structure

Rather than defining a large flat list of fixed thresholds, group alerts into three tiers.

### Critical candidates

```text
port state != ACTIVE
link_downed increase > 0
phy_link_down_events increase > 0

pause_storm severity=error increase > 0

pcie_outbound_buffer_overflow increase > 0
pcie_signal_integrity increase > 0

rp_cnp_ignored increase > 0

req/resp remote access error increase > 0
remote_invalid_request increase > 0
local_length_error increase > 0

any required scrape collector unusable
```

### Warning candidates

```text
local_ack_timeout_err sustained
packet_seq_err sustained
out_of_sequence sustained
rnr_nak_retry_err sustained
out_of_buffer sustained

PFC occupancy high
PCIe stall sustained

PCS symbol error
CRC error

pause storm warning
adaptive retransmission timeout
```

### Dashboard / anomaly-detection candidates

```text
throughput
packets/s

PFC frames
PFC transitions

ECN CE
CNP sent/handled
cc_* counters

FEC corrected-bit ratio
per-lane raw error rate

adaptive retransmission
slow restart

RDMA READ/WRITE/ATOMIC volume
```

## 25. Metrics that should not be simple ">0" alerts

This deserves emphasis on its own:

```text
PFC
ECN CE
CNP
FEC corrected bits
adaptive retransmission
port_xmit_wait
```

**None of these means "fault" purely by being non-zero.** In a lossless RoCE fabric, PFC and CNP activity as a *result of* congestion control doing its job is expected and healthy.

Evaluate these along dimensions like:

```text
absolute rate
+
duration
+
ratio (normalized to traffic)
+
deviation from baseline
+
correlation with actual performance degradation
```

rather than a bare threshold on the raw counter.

## 26. Classifying RoCE incidents by metric pattern

This table is effectively a runbook lookup and is worth keeping close at hand during an incident.

| Observed pattern | Primary suspect |
|---|---|
| PFC RX↑, ECN↑, CNP↑ | Fabric / downstream congestion |
| PFC RX↑, ECN≈0 | ECN threshold / configuration problem |
| PFC TX↑, RX buffer discards↑ | Host/NIC receive bottleneck |
| PFC TX↑, PCIe stall↑ | Host PCIe bottleneck |
| packet_seq_err↑, ACK timeout↑ | Packet loss / severe delay |
| RNR↑, QP out_of_buffer↑ | Receiver / application |
| FEC corrected↑ only | Degrading link, still within correction budget |
| FEC↑ + PCS↑ + CRC↑ | Physical link fault |
| Only lane N raw error↑ | Specific SerDes lane / cable |
| PCIe signal error↑ | Host-side PCIe physical fault |
| CNP ignored↑ | RoCE congestion-control configuration |
| Large CQE flush burst + another error immediately before it | The flush is a secondary symptom, not root cause |
| link error recovery↑ → link down↑ | Worsening physical link |

## 27. A recommended top-level Grafana overview

Putting every metric on one screen makes incident response harder, not easier. A practical overview limits itself to around ten panels:

| Panel | Base PromQL |
|---|---|
| Port state | `rdma_port_info` |
| RX/TX Gb/s | `32 * rate(rdma_port_{rcv,xmit}_data_total[...]) / 1e9` |
| PFC RX occupancy | `rate(rdma_roce_pfc_pause_duration_total{direction="rx"}[...]) / 1e6` |
| PFC TX occupancy | Same expression, `direction="tx"` |
| ECN CE rate | `rate(rdma_np_ecn_marked_roce_packets_total[...])` |
| ACK / sequence errors | `rate(rdma_local_ack_timeout_err_total[...]) + rate(rdma_packet_seq_err_total[...])` |
| RX buffer drops | `rate(rdma_netdev_*discard*[...])` |
| PCIe stall fraction | `rate(rdma_pcie_outbound_stalled_seconds_total[...])` |
| FEC corrected ratio | `increase(rdma_phy_rx_corrected_bits_total[...]) / clamp_min(increase(rdma_phy_rx_bits_total[...]), 1)` |
| Link/PHY hard errors | `link_downed` + PCS symbol errors + CRC errors |

This mirrors the layering used in the repository's Grafana dashboard design (`docs/rdma_exporter_grafana_dashboard_design.md`): overview → throughput → congestion/error deep-dive, with PFC occupancy, ECN/CNP, PCIe stall, and FEC ratio as the primary panels in that deep-dive tier.

## 28. The most important causal chain

The full `rdma_exporter` metric surface makes the most sense when placed on this causal chain:

```text
                     ┌──────────────────┐
                     │ Physical medium  │
                     │ cable / optics   │
                     └────────┬─────────┘
                              │
                  lane errors / FEC / CRC
                              │
                              ▼
                     ┌──────────────────┐
                     │ Ethernet fabric  │
                     │ switch / buffer  │
                     └────────┬─────────┘
                              │
                         ECN / PFC
                              │
                              ▼
                    ┌───────────────────┐
                    │ RoCE transport    │
                    │ PSN / ACK / CNP   │
                    └────────┬──────────┘
                              │
              timeout / seq error / retransmit
                              │
                              ▼
                    ┌───────────────────┐
                    │ RDMA QP / verbs   │
                    │ WQE / CQE / RNR   │
                    └────────┬──────────┘
                              │
                              ▼
                    ┌───────────────────┐
                    │ Application       │
                    │ NCCL / MPI / etc. │
                    └───────────────────┘


                              NIC
                               │
                               ▼
                    ┌───────────────────┐
                    │ PCIe / Host       │
                    │ stall / buffers   │
                    └───────────────────┘
```

This layout matters because the *same* end symptom — say, an NCCL collective timeout — can arise from entirely different root causes that require entirely different remediation:

```text
optics degrade
→ FEC/CRC errors
→ packet loss
→ packet_seq_err
→ retransmit
→ NCCL timeout
```

versus:

```text
PCIe stall
→ NIC RX buffer pressure
→ PFC TX
→ peer stalls
→ collective slows down
→ NCCL timeout
```

versus:

```text
receiver scheduling delay
→ receive WQE shortage
→ RNR
→ retransmit
→ NCCL timeout
```

`rdma_exporter`'s current metric set is built to distinguish these three failure domains from the host side with reasonably high confidence. The `ethtool` collector (default on) covers not just PFC but also buffer, PCIe, PHY/FEC, IEEE 802.3x global pause, pause storm, and vPort RDMA metrics. The optional RDMA-netlink collector is also on by default. Only the QP collector is off by default, given its cost on dense hosts.

## 29. Suggested next step

A natural extension of this document is a symptom-driven troubleshooting flow that is scoped to `rdma_exporter` alone — of the shape "symptom → PromQL to check → verdict → what to check next in `mlxlink` / `ethtool` / switch counters." That flow can reuse the pattern table in [Section 26](#26-classifying-roce-incidents-by-metric-pattern) as its backbone.
