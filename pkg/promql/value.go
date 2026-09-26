package promql

import (
	"sort"

	"github.com/nicktill/tinyobs/pkg/labels"
)

// Value is the result of evaluating an expression: Scalar, Vector, Matrix or String.
type Value interface {
	Type() ValueType
}

// Scalar is a single number at a time (ms).
type Scalar struct {
	T int64
	V float64
}

// String is a string literal result.
type String struct {
	T int64
	V string
}

// Sample is one element of an instant vector.
type Sample struct {
	Metric labels.Labels
	T      int64
	F      float64
	// DropName marks the metric name for removal. Names are dropped when
	// evaluation finishes rather than immediately ("delayed name removal"),
	// so label_replace can still read or keep __name__, and aggregations
	// over results of different metrics don't collide early.
	DropName bool
}

// Vector is an instant vector: at most one sample per label set.
type Vector []Sample

// Point is a timestamped value inside a series.
type Point struct {
	T int64
	F float64
}

// Series is a label set with points in time order.
type Series struct {
	Metric labels.Labels
	Points []Point
}

// Matrix is a set of series: a range vector, or the result of a range query.
type Matrix []Series

func (Scalar) Type() ValueType { return TypeScalar }
func (String) Type() ValueType { return TypeString }
func (Vector) Type() ValueType { return TypeVector }
func (Matrix) Type() ValueType { return TypeMatrix }

func sortMatrix(m Matrix) {
	sort.Slice(m, func(i, j int) bool { return labels.Compare(m[i].Metric, m[j].Metric) < 0 })
}
