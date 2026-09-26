"""Generates the OTLP fixtures used by the tests, with the official
OpenTelemetry protobuf classes, so the decoder is checked against real
encodings rather than against itself.

    python -m venv venv && venv/bin/pip install opentelemetry-proto
    venv/bin/python pkg/otlp/testdata/gen.py
"""
import os

from google.protobuf import json_format
from opentelemetry.proto.collector.metrics.v1 import metrics_service_pb2 as svc
from opentelemetry.proto.common.v1 import common_pb2 as common
from opentelemetry.proto.metrics.v1 import metrics_pb2 as m

T = 1_700_000_000_000_000_000  # nanoseconds
CUMULATIVE = m.AGGREGATION_TEMPORALITY_CUMULATIVE
DELTA = m.AGGREGATION_TEMPORALITY_DELTA


def kv(key, **value):
    return common.KeyValue(key=key, value=common.AnyValue(**value))


req = svc.ExportMetricsServiceRequest()
rm = req.resource_metrics.add()
rm.resource.attributes.extend([
    kv("service.name", string_value="checkout"),
    kv("service.namespace", string_value="shop"),
    kv("service.instance.id", string_value="pod-1"),
    kv("deployment.environment", string_value="dev"),
])
sm = rm.scope_metrics.add()
sm.scope.name = "example"

requests = sm.metrics.add(name="http.server.requests", unit="{request}", description="Requests.")
requests.sum.aggregation_temporality = CUMULATIVE
requests.sum.is_monotonic = True
for status, v in ((200, 42), (500, 3)):
    p = requests.sum.data_points.add(time_unix_nano=T, as_int=v)
    p.attributes.extend([kv("http.route", string_value="/cart"), kv("http.response.status_code", int_value=status)])

mem = sm.metrics.add(name="process.memory.usage", unit="By")
mem.gauge.data_points.add(time_unix_nano=T, as_double=1048576.0)

dur = sm.metrics.add(name="http.server.request.duration", unit="s")
dur.histogram.aggregation_temporality = CUMULATIVE
p = dur.histogram.data_points.add(time_unix_nano=T, count=10, sum=3.25, bucket_counts=[5, 3, 1, 1], explicit_bounds=[0.1, 0.5, 1])
p.attributes.extend([kv("http.route", string_value="/cart")])

rpc = sm.metrics.add(name="rpc.latency", unit="ms")
p = rpc.summary.data_points.add(time_unix_nano=T, count=7, sum=1.25)
p.quantile_values.add(quantile=0.5, value=0.125)
p.quantile_values.add(quantile=0.99, value=0.875)

delta = sm.metrics.add(name="jobs.processed")
delta.sum.aggregation_temporality = DELTA
delta.sum.is_monotonic = True
delta.sum.data_points.add(time_unix_nano=T, as_int=5)

exp = sm.metrics.add(name="exp.hist")
exp.exponential_histogram.aggregation_temporality = CUMULATIVE
exp.exponential_histogram.data_points.add(time_unix_nano=T, count=1)

queue = sm.metrics.add(name="queue.depth")
queue.gauge.data_points.add(time_unix_nano=T, as_double=0, flags=m.DATA_POINT_FLAGS_NO_RECORDED_VALUE_MASK)

cpu = sm.metrics.add(name="cpu.utilization", unit="1")
p = cpu.gauge.data_points.add(time_unix_nano=T, as_double=0.5)
p.attributes.extend([kv("0weird", bool_value=True), kv("net.peer.name", string_value="db"), kv("tags", array_value=common.ArrayValue(values=[common.AnyValue(string_value="a"), common.AnyValue(int_value=1)]))])

here = os.path.dirname(os.path.abspath(__file__))
with open(os.path.join(here, "metrics.pb"), "wb") as f:
    f.write(req.SerializeToString())
with open(os.path.join(here, "metrics.json"), "w") as f:
    f.write(json_format.MessageToJson(req))
