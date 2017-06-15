package query

import (
	"fmt"
	"sync"

	"github.com/influxdata/influxdb/influxql"
)

// Edge connects two nodes in a directed graph. The Edge contains an input
// Node, which is where it receives its input from. The Edge then holds onto
// the iterator created by the Node so it can be sent to the output Node.
//
// Every Edge is in a non-ready state or a ready state. When it is in a
// non-ready state, it is still waiting for its input node to send the edge its
// iterator. When it is in a ready state, the iterator is ready to be consumed
// by the output node.
type Edge struct {
	// Input is the Node that creates the Iterator for this Edge.
	Input Node

	// Output is the Node that will receive the Iterator created for this Edge.
	// An edge does not need to have an output edge.
	Output Node

	itr   influxql.Iterator
	ready bool
	mu    sync.RWMutex
}

// Iterator returns the Iterator created for this Node by the Input edge.
// If the Node returns false from Ready(), this function will panic.
func (e *Edge) Iterator() influxql.Iterator {
	e.mu.RLock()
	if !e.ready {
		e.mu.RUnlock()
		panic("attempted to retrieve an iterator from an edge before it was ready")
	}
	itr := e.itr
	e.mu.RUnlock()
	return itr
}

// SetIterator marks this Edge as ready and sets the Iterator as the returned
// iterator. If the Edge has already been set, this panics. This should only be
// called from the Input Node.
func (e *Edge) SetIterator(itr influxql.Iterator) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.ready {
		panic("unable to call SetIterator on the same node twice")
	}
	e.itr = itr
	e.ready = true
}

func (e *Edge) Ready() (ready bool) {
	e.mu.RLock()
	ready = e.ready
	e.mu.RUnlock()
	return ready
}

type Node interface {
	// Description returns a brief description about what this node does.  This
	// should include details that describe what the node will do based on the
	// current configuration of the node.
	Description() string

	// Inputs returns the Edges that produce Iterators that will be consumed by
	// this Node.
	Inputs() []*Edge

	// Outputs returns the Edges that will receive an Iterator from this Node.
	Outputs() []*Edge

	// Execute executes the Node and transmits the created Iterators to the
	// output edges.
	Execute(plan *Plan) error
}

// AllInputsReady determines if all of the input edges for a node are ready.
func AllInputsReady(n Node) bool {
	inputs := n.Inputs()
	if len(inputs) == 0 {
		return true
	}

	for _, input := range inputs {
		if !input.Ready() {
			return false
		}
	}
	return true
}

type IteratorCreator struct {
	Measurement *influxql.Measurement
	Output      *Edge
}

func (ic *IteratorCreator) Description() string {
	return fmt.Sprintf("create iterator for %s", ic.Measurement)
}

func (ic *IteratorCreator) Inputs() []*Edge  { return nil }
func (ic *IteratorCreator) Outputs() []*Edge { return []*Edge{ic.Output} }

func (ic *IteratorCreator) Execute(plan *Plan) error {
	// Create a merge node that all of our generated inputs will go into.
	merge := &Merge{
		Output: ic.Output,
	}
	merge.Output.Input = merge

	// Lookup the shards.
	shards := make([]*Edge, 0, 3)
	for _, id := range []uint{1, 2, 3} {
		sh := &ShardIteratorCreator{
			Ref:     ic.Measurement.Name,
			ShardID: id,
			Output:  &Edge{},
		}
		sh.Output.Input = sh
		sh.Output.Output = merge
		shards = append(shards, sh.Output)
	}
	merge.InputNodes = shards

	nodes := make([]Node, 0, len(shards))
	for _, sh := range shards {
		nodes = append(nodes, sh.Input)
	}
	plan.ScheduleWork(nodes...)
	return nil
}

type ShardIteratorCreator struct {
	Ref     string
	ShardID uint
	Output  *Edge
}

func (sh *ShardIteratorCreator) Description() string {
	return fmt.Sprintf("create iterator for %s [shard %d]", sh.Ref, sh.ShardID)
}

func (sh *ShardIteratorCreator) Inputs() []*Edge  { return nil }
func (sh *ShardIteratorCreator) Outputs() []*Edge { return []*Edge{sh.Output} }

func (sh *ShardIteratorCreator) Execute(plan *Plan) error {
	if plan.DryRun {
		sh.Output.SetIterator(nil)
		return nil
	}
	return nil
}

type Merge struct {
	InputNodes []*Edge
	Output     *Edge
}

func (m *Merge) Description() string {
	return fmt.Sprintf("merge %d nodes", len(m.InputNodes))
}

func (m *Merge) Inputs() []*Edge  { return m.InputNodes }
func (m *Merge) Outputs() []*Edge { return []*Edge{m.Output} }

func (m *Merge) Execute(plan *Plan) error {
	if plan.DryRun {
		m.Output.SetIterator(nil)
		return nil
	}
	return nil
}

type FunctionCall struct {
	Name   string
	Input  *Edge
	Output *Edge
}

func (c *FunctionCall) Description() string {
	return fmt.Sprintf("%s()", c.Name)
}

func (c *FunctionCall) Inputs() []*Edge  { return []*Edge{c.Input} }
func (c *FunctionCall) Outputs() []*Edge { return []*Edge{c.Output} }

func (c *FunctionCall) Execute(plan *Plan) error {
	if plan.DryRun {
		c.Output.SetIterator(nil)
		return nil
	}
	return nil
}
