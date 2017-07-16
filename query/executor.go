package query

import (
	"io"
	"time"

	"github.com/influxdata/influxdb/influxql"
	"github.com/influxdata/influxdb/services/meta"
	"github.com/influxdata/influxdb/tsdb"
)

// IteratorCreator is an interface that combines mapping fields and creating iterators.
type ShardGroup interface {
	influxql.IteratorCreator
	influxql.FieldMapper
	io.Closer
}

type Plan struct {
	DryRun bool

	MetaClient interface {
		ShardsByTimeRange(sources influxql.Sources, tmin, tmax time.Time) (a []meta.ShardInfo, err error)
	}

	TSDBStore interface {
		ShardGroup(ids []uint64) tsdb.ShardGroup
	}

	ShardMapper interface {
		MapShards(sources influxql.Sources, opt *influxql.SelectOptions) (ShardGroup, error)
	}

	ready map[Node]struct{}
	want  map[*ReadEdge]struct{}
}

func NewPlan() *Plan {
	return &Plan{
		ready: make(map[Node]struct{}),
		want:  make(map[*ReadEdge]struct{}),
	}
}

func (p *Plan) AddTarget(e *ReadEdge) {
	if _, ok := p.want[e]; ok {
		return
	}
	p.want[e] = struct{}{}

	if node, ok := e.Input.Node.(OptimizableNode); ok {
		node.Optimize()
	}

	if inputs := e.Input.Node.Inputs(); len(inputs) == 0 {
		p.ready[e.Input.Node] = struct{}{}
		return
	} else {
		for _, input := range inputs {
			p.AddTarget(input)
		}
	}
}

func (p *Plan) FindWork() Node {
	for n := range p.ready {
		delete(p.ready, n)
		return n
	}
	return nil
}

func (p *Plan) ScheduleWork(nodes ...Node) {
	for _, n := range nodes {
		if AllInputsReady(n) {
			p.ready[n] = struct{}{}
			continue
		}

		// Add each input edge as a target.
		for _, input := range n.Inputs() {
			p.AddTarget(input)
		}
	}
}

// EdgeFinished runs when notified that an Edge has finished running so the
// Edge's output Nodes can be checked to see if their output edges are now
// ready to be run.
func (p *Plan) NodeFinished(n Node) {
	for _, e := range n.Outputs() {
		// The nodes are now considered ready. Check if their output edge is
		// now ready to be executed (if they have one).
		if e.Output.Node != nil && AllInputsReady(e.Output.Node) {
			p.ready[e.Output.Node] = struct{}{}
		}
	}
}
