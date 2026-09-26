#!/bin/sh
# Downloads Prometheus's PromQL test files, which the conformance test in
# pkg/promql runs against the TinyObs engine. They are Apache-2.0 licensed and
# are fetched rather than committed.
set -eu

VERSION=v3.7.0
DIR="$(dirname "$0")/../pkg/promql/testdata/prometheus"
FILES="aggregators at_modifier collision duration_expression functions histograms limit literals
name_label_dropping native_histograms operators range_queries selectors staleness subquery trig_functions"

mkdir -p "$DIR"
for f in $FILES; do
	curl -fsSL -o "$DIR/$f.test" \
		"https://raw.githubusercontent.com/prometheus/prometheus/$VERSION/promql/promqltest/testdata/$f.test"
done
echo "$VERSION" > "$DIR/VERSION"
echo "Fetched Prometheus $VERSION PromQL tests into $DIR"
