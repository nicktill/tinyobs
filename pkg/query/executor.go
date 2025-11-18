package query

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
	"github.com/nicktill/tinyobs/pkg/storage"
)

// Executor executes parsed query expressions against storage
type Executor struct {
	storage storage.Storage
}

// NewExecutor creates a new query executor
func NewExecutor(store storage.Storage) *Executor {
	return &Executor{storage: store}
}

// Execute executes a query and returns time series data
func (e *Executor) Execute(ctx context.Context, query *Query) (*Result, error) {
	return e.executeExpr(ctx, query.Expr, query.Start, query.End, query.Step)
}

// Result represents the result of a query execution
type Result struct {
	Series []TimeSeries
}

// TimeSeries represents a single time series with values over time
type TimeSeries struct {
	Labels map[string]string
	Points []Point
}

// Point represents a single data point (timestamp, value)
type Point struct {
	Time  time.Time
	Value float64
}

// executeExpr executes an expression and returns a result
func (e *Executor) executeExpr(ctx context.Context, expr Expr, start, end time.Time, step time.Duration) (*Result, error) {
	switch ex := expr.(type) {
	case *VectorSelector:
		return e.executeVectorSelector(ctx, ex, start, end)
	case *RangeSelector:
		return e.executeRangeSelector(ctx, ex, start, end, step)
	case *BinaryExpr:
		return e.executeBinaryExpr(ctx, ex, start, end, step)
	case *AggregateExpr:
		return e.executeAggregateExpr(ctx, ex, start, end, step)
	case *FunctionCall:
		return e.executeFunctionCall(ctx, ex, start, end, step)
	case *NumberLiteral:
		return e.executeNumberLiteral(ctx, ex, start, end, step)
	case *UnaryExpr:
		return e.executeUnaryExpr(ctx, ex, start, end, step)
	case *ParenExpr:
		return e.executeExpr(ctx, ex.Expr, start, end, step)
	default:
		return nil, fmt.Errorf("unsupported expression type: %T", expr)
	}
}

// executeVectorSelector executes a vector selector query
func (e *Executor) executeVectorSelector(ctx context.Context, vec *VectorSelector, start, end time.Time) (*Result, error) {
	// Build query request
	req := storage.QueryRequest{
		Start:       start,
		End:         end,
		MetricNames: []string{vec.Name},
		Labels:      make(map[string]string),
	}

	// Add label filters (only exact matches for now, regex later)
	for _, matcher := range vec.Matchers {
		if matcher.Op == TokenEqual {
			req.Labels[matcher.Name] = matcher.Value
		}
	}

	// Query storage
	metricsData, err := e.storage.Query(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("storage query failed: %w", err)
	}

	// Group metrics by label set into time series
	seriesMap := make(map[string]*TimeSeries)
	for _, m := range metricsData {
		key := e.seriesKey(m.Labels)
		if _, exists := seriesMap[key]; !exists {
			seriesMap[key] = &TimeSeries{
				Labels: m.Labels,
				Points: []Point{},
			}
		}
		seriesMap[key].Points = append(seriesMap[key].Points, Point{
			Time:  m.Timestamp,
			Value: m.Value,
		})
	}

	// Convert map to slice
	series := make([]TimeSeries, 0, len(seriesMap))
	for _, ts := range seriesMap {
		// Sort points by time
		sort.Slice(ts.Points, func(i, j int) bool {
			return ts.Points[i].Time.Before(ts.Points[j].Time)
		})
		series = append(series, *ts)
	}

	return &Result{Series: series}, nil
}

// executeRangeSelector executes a range selector (returns raw data for functions like rate)
func (e *Executor) executeRangeSelector(ctx context.Context, r *RangeSelector, start, end time.Time, step time.Duration) (*Result, error) {
	// For range selectors, we need to fetch data from (start - duration) to end
	// This allows functions like rate() to calculate values at the start time
	adjustedStart := start.Add(-r.Duration)
	return e.executeVectorSelector(ctx, r.Vector, adjustedStart, end)
}

// executeBinaryExpr executes a binary expression
func (e *Executor) executeBinaryExpr(ctx context.Context, bin *BinaryExpr, start, end time.Time, step time.Duration) (*Result, error) {
	// Execute left and right sides
	left, err := e.executeExpr(ctx, bin.Left, start, end, step)
	if err != nil {
		return nil, err
	}

	right, err := e.executeExpr(ctx, bin.Right, start, end, step)
	if err != nil {
		return nil, err
	}

	// Apply operator
	return e.applyBinaryOp(left, right, bin.Op)
}

// applyBinaryOp applies a binary operator to two results
func (e *Executor) applyBinaryOp(left, right *Result, op TokenType) (*Result, error) {
	// Simple case: scalar operations
	if len(left.Series) == 1 && len(right.Series) == 1 {
		result := &Result{Series: []TimeSeries{}}
		ts := TimeSeries{
			Labels: left.Series[0].Labels,
			Points: []Point{},
		}

		// Match points by timestamp and apply operation
		for _, lp := range left.Series[0].Points {
			for _, rp := range right.Series[0].Points {
				if lp.Time.Equal(rp.Time) {
					val := e.applyOp(lp.Value, rp.Value, op)
					ts.Points = append(ts.Points, Point{Time: lp.Time, Value: val})
					break
				}
			}
		}

		result.Series = append(result.Series, ts)
		return result, nil
	}

	// TODO: Implement vector matching for many-to-many operations
	return nil, fmt.Errorf("vector-to-vector operations not yet implemented")
}

// applyOp applies an arithmetic operator
func (e *Executor) applyOp(left, right float64, op TokenType) float64 {
	switch op {
	case TokenPlus:
		return left + right
	case TokenMinus:
		return left - right
	case TokenMultiply:
		return left * right
	case TokenDivide:
		if right != 0 {
			return left / right
		}
		return math.NaN()
	case TokenPower:
		return math.Pow(left, right)
	case TokenMod:
		return math.Mod(left, right)
	default:
		return math.NaN()
	}
}

// executeAggregateExpr executes an aggregation expression
func (e *Executor) executeAggregateExpr(ctx context.Context, agg *AggregateExpr, start, end time.Time, step time.Duration) (*Result, error) {
	// Execute inner expression
	inner, err := e.executeExpr(ctx, agg.Expr, start, end, step)
	if err != nil {
		return nil, err
	}

	// Group series by labels
	groups := e.groupSeries(inner.Series, agg.Grouping, agg.Without)

	// Apply aggregation to each group
	result := &Result{Series: []TimeSeries{}}
	for labels, series := range groups {
		ts := TimeSeries{
			Labels: labels,
			Points: e.aggregate(series, agg.Op),
		}
		result.Series = append(result.Series, ts)
	}

	return result, nil
}

// groupSeries groups time series by specified labels
func (e *Executor) groupSeries(series []TimeSeries, grouping []string, without bool) map[map[string]string][]TimeSeries {
	groups := make(map[string][]TimeSeries)
	groupLabels := make(map[string]map[string]string)

	for _, ts := range series {
		// Build group key based on grouping labels
		var key string
		labels := make(map[string]string)

		if without {
			// Include all labels except those in grouping
			for k, v := range ts.Labels {
				skip := false
				for _, g := range grouping {
					if k == g {
						skip = true
						break
					}
				}
				if !skip {
					labels[k] = v
					key += k + "=" + v + ","
				}
			}
		} else {
			// Include only labels in grouping
			for _, g := range grouping {
				if v, ok := ts.Labels[g]; ok {
					labels[g] = v
					key += g + "=" + v + ","
				}
			}
		}

		groups[key] = append(groups[key], ts)
		groupLabels[key] = labels
	}

	// Convert to map[labels]series
	result := make(map[map[string]string][]TimeSeries)
	for key, series := range groups {
		result[groupLabels[key]] = series
	}

	return result
}

// aggregate applies an aggregation function to a group of time series
func (e *Executor) aggregate(series []TimeSeries, op string) []Point {
	if len(series) == 0 {
		return []Point{}
	}

	// Collect all unique timestamps
	timeMap := make(map[time.Time][]float64)
	for _, ts := range series {
		for _, p := range ts.Points {
			timeMap[p.Time] = append(timeMap[p.Time], p.Value)
		}
	}

	// Aggregate at each timestamp
	points := []Point{}
	for t, values := range timeMap {
		var aggValue float64
		switch op {
		case "sum":
			aggValue = e.sum(values)
		case "avg":
			aggValue = e.avg(values)
		case "max":
			aggValue = e.max(values)
		case "min":
			aggValue = e.min(values)
		case "count":
			aggValue = float64(len(values))
		default:
			aggValue = math.NaN()
		}
		points = append(points, Point{Time: t, Value: aggValue})
	}

	// Sort by time
	sort.Slice(points, func(i, j int) bool {
		return points[i].Time.Before(points[j].Time)
	})

	return points
}

// Aggregation helper functions
func (e *Executor) sum(values []float64) float64 {
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum
}

func (e *Executor) avg(values []float64) float64 {
	if len(values) == 0 {
		return math.NaN()
	}
	return e.sum(values) / float64(len(values))
}

func (e *Executor) max(values []float64) float64 {
	if len(values) == 0 {
		return math.NaN()
	}
	max := values[0]
	for _, v := range values[1:] {
		if v > max {
			max = v
		}
	}
	return max
}

func (e *Executor) min(values []float64) float64 {
	if len(values) == 0 {
		return math.NaN()
	}
	min := values[0]
	for _, v := range values[1:] {
		if v < min {
			min = v
		}
	}
	return min
}

// executeFunctionCall executes a function call
func (e *Executor) executeFunctionCall(ctx context.Context, fn *FunctionCall, start, end time.Time, step time.Duration) (*Result, error) {
	switch fn.Name {
	case "rate":
		return e.executeRate(ctx, fn, start, end, step)
	case "increase":
		return e.executeIncrease(ctx, fn, start, end, step)
	default:
		return nil, fmt.Errorf("unsupported function: %s", fn.Name)
	}
}

// executeRate calculates the per-second rate of increase
func (e *Executor) executeRate(ctx context.Context, fn *FunctionCall, start, end time.Time, step time.Duration) (*Result, error) {
	if len(fn.Args) != 1 {
		return nil, fmt.Errorf("rate() requires exactly 1 argument, got %d", len(fn.Args))
	}

	// Argument must be a range selector
	rangeExpr, ok := fn.Args[0].(*RangeSelector)
	if !ok {
		return nil, fmt.Errorf("rate() requires a range vector argument")
	}

	// Execute range selector
	data, err := e.executeRangeSelector(ctx, rangeExpr, start, end, step)
	if err != nil {
		return nil, err
	}

	// Calculate rate for each series
	result := &Result{Series: []TimeSeries{}}
	for _, ts := range data.Series {
		rateSeries := TimeSeries{
			Labels: ts.Labels,
			Points: []Point{},
		}

		// For each point, calculate rate using the range duration
		duration := rangeExpr.Duration.Seconds()
		for i := 0; i < len(ts.Points); i++ {
			// Find the point 'duration' seconds ago
			rangeStart := ts.Points[i].Time.Add(-rangeExpr.Duration)
			var startPoint *Point
			for j := i - 1; j >= 0; j-- {
				if ts.Points[j].Time.Before(rangeStart) || ts.Points[j].Time.Equal(rangeStart) {
					startPoint = &ts.Points[j]
					break
				}
			}

			if startPoint != nil {
				// Calculate rate: (current - start) / duration
				rate := (ts.Points[i].Value - startPoint.Value) / duration
				if rate < 0 {
					rate = 0 // Counter reset, treat as 0
				}
				rateSeries.Points = append(rateSeries.Points, Point{
					Time:  ts.Points[i].Time,
					Value: rate,
				})
			}
		}

		result.Series = append(result.Series, rateSeries)
	}

	return result, nil
}

// executeIncrease calculates the total increase over a time range
func (e *Executor) executeIncrease(ctx context.Context, fn *FunctionCall, start, end time.Time, step time.Duration) (*Result, error) {
	// increase() is rate() * duration
	rateResult, err := e.executeRate(ctx, fn, start, end, step)
	if err != nil {
		return nil, err
	}

	// Get duration from range selector
	rangeExpr := fn.Args[0].(*RangeSelector)
	duration := rangeExpr.Duration.Seconds()

	// Multiply all rate values by duration
	for i := range rateResult.Series {
		for j := range rateResult.Series[i].Points {
			rateResult.Series[i].Points[j].Value *= duration
		}
	}

	return rateResult, nil
}

// executeNumberLiteral returns a constant value
func (e *Executor) executeNumberLiteral(ctx context.Context, num *NumberLiteral, start, end time.Time, step time.Duration) (*Result, error) {
	// Generate points at each step
	points := []Point{}
	for t := start; !t.After(end); t = t.Add(step) {
		points = append(points, Point{Time: t, Value: num.Value})
	}

	return &Result{
		Series: []TimeSeries{
			{
				Labels: map[string]string{},
				Points: points,
			},
		},
	}, nil
}

// executeUnaryExpr executes a unary expression
func (e *Executor) executeUnaryExpr(ctx context.Context, unary *UnaryExpr, start, end time.Time, step time.Duration) (*Result, error) {
	inner, err := e.executeExpr(ctx, unary.Expr, start, end, step)
	if err != nil {
		return nil, err
	}

	// Apply unary operator to all values
	if unary.Op == TokenMinus {
		for i := range inner.Series {
			for j := range inner.Series[i].Points {
				inner.Series[i].Points[j].Value = -inner.Series[i].Points[j].Value
			}
		}
	}
	// TokenPlus doesn't change anything

	return inner, nil
}

// seriesKey creates a unique key for a time series based on labels
func (e *Executor) seriesKey(labels map[string]string) string {
	// Sort labels for consistent key
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	key := ""
	for _, k := range keys {
		key += k + "=" + labels[k] + ","
	}
	return key
}
