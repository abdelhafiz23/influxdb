package query

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/influxdata/influxdb/influxql"
)

// InputEdge is the input end of an edge.
type InputEdge struct {
	// Node is the node that creates an Iterator and sends it to this edge.
	// This should always be set to a value.
	Node Node

	// Output is the output end of the edge. This should always be set.
	Output *OutputEdge

	itr   influxql.Iterator
	ready bool
	mu    sync.RWMutex
}

// SetIterator marks this Edge as ready and sets the Iterator as the returned
// iterator. If the Edge has already been set, this panics. The result can be
// retrieved from the output edge.
func (e *InputEdge) SetIterator(itr influxql.Iterator) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.ready {
		panic("unable to call SetIterator on the same node twice")
	}
	e.itr = itr
	e.ready = true
}

// Insert splits the current edge and inserts a Node into the middle.
// It then returns the newly created OutputEdge that points to the inserted
// Node and the newly created InputEdge that the Node should use to send its
// results.
func (e *InputEdge) Insert(n Node) (*OutputEdge, *InputEdge) {
	// Create a new InputEdge. The output should be the old location this
	// InputEdge pointed to.
	in := &InputEdge{Node: n, Output: e.Output}
	// Reset the OutputEdge so it points to the newly created input as its input.
	e.Output.Input = in
	// Redirect this InputEdge's output to a new output edge.
	e.Output = &OutputEdge{Node: n, Input: e}
	// Return the newly created edges so they can be stored with the newly
	// inserted Node.
	return e.Output, in
}

// OutputEdge is the output end of an edge.
type OutputEdge struct {
	// Node is the node that will read the Iterator from this edge.
	// This may be nil if there is no Node that will read this edge.
	Node Node

	// Input is the input end of the edge. This should always be set.
	Input *InputEdge
}

// Iterator returns the Iterator created for this Node by the InputEdge.
// If the InputEdge is not ready, this function will panic.
func (e *OutputEdge) Iterator() influxql.Iterator {
	e.Input.mu.RLock()
	if !e.Input.ready {
		e.Input.mu.RUnlock()
		panic("attempted to retrieve an iterator from an edge before it was ready")
	}
	itr := e.Input.itr
	e.Input.mu.RUnlock()
	return itr
}

// Ready returns whether this OutputEdge is ready to be read from. This edge
// will be ready after the attached InputEdge has called SetIterator().
func (e *OutputEdge) Ready() (ready bool) {
	e.Input.mu.RLock()
	ready = e.Input.ready
	e.Input.mu.RUnlock()
	return ready
}

// Append sets the Node for the current output edge and then creates a new Edge
// that points to nothing.
func (e *OutputEdge) Append(out Node) (*InputEdge, *OutputEdge) {
	e.Node = out
	return NewEdge(out)
}

// NewEdge creates a new edge with the input node set to the argument and the
// output node set to nothing.
func NewEdge(in Node) (*InputEdge, *OutputEdge) {
	return AddEdge(in, nil)
}

// AddEdge creates a new edge between two nodes.
func AddEdge(in, out Node) (*InputEdge, *OutputEdge) {
	input := &InputEdge{Node: in}
	output := &OutputEdge{Node: out}
	input.Output, output.Input = output, input
	return input, output
}

/*
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

// NewEdge creates a new edge with the input node set to the argument and the
// output node set to nothing.
func NewEdge(in Node) *Edge {
	return &Edge{Input: in}
}

// AddEdge creates a new edge with the input and output node. It returns the
// same edge twice so the same edge can be assigned to the output edge of the
// input node and the input edge of the output node by the caller.
func AddEdge(in, out Node) (*Edge, *Edge) {
	edge := NewEdge(in)
	edge.Output = out
	return edge, edge
}

// Chain takes a node along with its input and output node addresses. It
// assigns the edge to the node's input (and sets the edge's output to the
// node). It then creates a new edge that has the node as the input and returns
// the new edge.
func (e *Edge) Chain(node Node, in, out **Edge) *Edge {
	e.Output = node
	*in = e
	*out = NewEdge(node)
	return *out
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
*/

type Node interface {
	// Description returns a brief description about what this node does.  This
	// should include details that describe what the node will do based on the
	// current configuration of the node.
	Description() string

	// Inputs returns the Edges that produce Iterators that will be consumed by
	// this Node.
	Inputs() []*OutputEdge

	// Outputs returns the Edges that will receive an Iterator from this Node.
	Outputs() []*InputEdge

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

var _ Node = &IteratorCreator{}

type IteratorCreator struct {
	Expr        influxql.Expr
	Aux         []influxql.VarRef
	Measurement *influxql.Measurement
	Output      *InputEdge
}

func (ic *IteratorCreator) Description() string {
	return fmt.Sprintf("create iterator for %s", ic.Measurement)
}

func (ic *IteratorCreator) Inputs() []*OutputEdge { return nil }
func (ic *IteratorCreator) Outputs() []*InputEdge {
	if ic.Output != nil {
		return []*InputEdge{ic.Output}
	}
	return nil
}

func (ic *IteratorCreator) Execute(plan *Plan) error {
	if plan.MetaClient == nil {
		if !plan.DryRun {
			return errors.New("no meta client set")
		}
	}

	start, end := time.Unix(0, influxql.MinTime), time.Unix(0, influxql.MaxTime)
	shards, err := plan.MetaClient.ShardsByTimeRange(influxql.Sources{ic.Measurement}, start, end)
	if err != nil {
		return err
	}

	// Create a merge node that all of our generated inputs will go into. Set
	// the output of the Merge node to where the output of this node was
	// supposed to go.
	merge := &Merge{
		Output: ic.Output,
	}
	merge.Output.Node = merge

	// Lookup the shards.
	for _, shardInfo := range shards {
		sh := &ShardIteratorCreator{
			Expr:    ic.Expr,
			Aux:     ic.Aux,
			Ref:     ic.Measurement.Name,
			ShardID: shardInfo.ID,
		}
		sh.Output = merge.AddInput(sh)
	}
	ic.Output = nil
	plan.ScheduleWork(merge)
	return nil
}

var _ Node = &ShardIteratorCreator{}

type ShardIteratorCreator struct {
	Expr    influxql.Expr
	Aux     []influxql.VarRef
	Ref     string
	ShardID uint64
	Output  *InputEdge
}

func (sh *ShardIteratorCreator) Description() string {
	return fmt.Sprintf("create iterator for %s [shard %d]", sh.Ref, sh.ShardID)
}

func (sh *ShardIteratorCreator) Inputs() []*OutputEdge { return nil }
func (sh *ShardIteratorCreator) Outputs() []*InputEdge { return []*InputEdge{sh.Output} }

func (sh *ShardIteratorCreator) Execute(plan *Plan) error {
	if plan.DryRun {
		sh.Output.SetIterator(nil)
		return nil
	}

	shard := plan.TSDBStore.ShardGroup([]uint64{sh.ShardID})
	opt := influxql.IteratorOptions{
		Expr:      sh.Expr,
		Aux:       sh.Aux,
		StartTime: influxql.MinTime,
		EndTime:   influxql.MaxTime,
		Ascending: true,
	}
	itr, err := shard.CreateIterator(sh.Ref, opt)
	if err != nil {
		return err
	}
	sh.Output.SetIterator(itr)
	return nil
}

var _ Node = &Merge{}

type Merge struct {
	InputNodes []*OutputEdge
	Output     *InputEdge
}

func (m *Merge) Description() string {
	return fmt.Sprintf("merge %d nodes", len(m.InputNodes))
}

func (m *Merge) AddInput(n Node) *InputEdge {
	in, out := AddEdge(n, m)
	m.InputNodes = append(m.InputNodes, out)
	return in
}

func (m *Merge) Inputs() []*OutputEdge { return m.InputNodes }
func (m *Merge) Outputs() []*InputEdge { return []*InputEdge{m.Output} }

func (m *Merge) Execute(plan *Plan) error {
	if plan.DryRun {
		m.Output.SetIterator(nil)
		return nil
	}

	if len(m.InputNodes) == 0 {
		m.Output.SetIterator(nil)
		return nil
	} else if len(m.InputNodes) == 1 {
		m.Output.SetIterator(m.InputNodes[0].Iterator())
		return nil
	}

	inputs := make([]influxql.Iterator, len(m.InputNodes))
	for i, input := range m.InputNodes {
		inputs[i] = input.Iterator()
	}
	itr := influxql.NewSortedMergeIterator(inputs, influxql.IteratorOptions{Ascending: true})
	m.Output.SetIterator(itr)
	return nil
}

var _ Node = &FunctionCall{}

type FunctionCall struct {
	Name   string
	Input  *OutputEdge
	Output *InputEdge
}

func (c *FunctionCall) Description() string {
	return fmt.Sprintf("%s()", c.Name)
}

func (c *FunctionCall) Inputs() []*OutputEdge { return []*OutputEdge{c.Input} }
func (c *FunctionCall) Outputs() []*InputEdge { return []*InputEdge{c.Output} }

func (c *FunctionCall) Execute(plan *Plan) error {
	if plan.DryRun {
		c.Output.SetIterator(nil)
		return nil
	}
	return nil
}

type AuxiliaryFields struct {
	Aux     []influxql.VarRef
	Input   *OutputEdge
	outputs []*InputEdge
	refs    []influxql.VarRef
}

func (c *AuxiliaryFields) Description() string {
	return "access auxiliary fields"
}

func (c *AuxiliaryFields) Inputs() []*OutputEdge { return []*OutputEdge{c.Input} }
func (c *AuxiliaryFields) Outputs() []*InputEdge { return c.outputs }

func (c *AuxiliaryFields) Execute(plan *Plan) error {
	if plan.DryRun {
		for _, output := range c.outputs {
			output.SetIterator(nil)
		}
		return nil
	}

	opt := influxql.IteratorOptions{Aux: c.Aux}
	aitr := influxql.NewAuxIterator(c.Input.Iterator(), opt)
	for i, ref := range c.refs {
		itr := aitr.Iterator(ref.Val, ref.Type)
		c.outputs[i].SetIterator(itr)
	}
	aitr.Background()
	return nil
}

func (c *AuxiliaryFields) Iterator(ref *influxql.VarRef) *OutputEdge {
	in, out := NewEdge(c)
	c.outputs = append(c.outputs, in)
	c.refs = append(c.refs, *ref)
	return out
}

var _ Node = &BinaryExpr{}

type BinaryExpr struct {
	LHS, RHS *OutputEdge
	Output   *InputEdge
	Op       influxql.Token
	Desc     string
}

func (c *BinaryExpr) Description() string {
	return c.Desc
}

func (c *BinaryExpr) Inputs() []*OutputEdge { return []*OutputEdge{c.LHS, c.RHS} }
func (c *BinaryExpr) Outputs() []*InputEdge { return []*InputEdge{c.Output} }

func (c *BinaryExpr) Execute(plan *Plan) error {
	if plan.DryRun {
		c.Output.SetIterator(nil)
		return nil
	}
	return nil
}

var _ Node = &Limit{}

type Limit struct {
	Input  *OutputEdge
	Output *InputEdge

	Limit  int
	Offset int
}

func (c *Limit) Description() string {
	if c.Limit > 0 && c.Offset > 0 {
		return fmt.Sprintf("limit %d/offset %d", c.Limit, c.Offset)
	} else if c.Limit > 0 {
		return fmt.Sprintf("limit %d", c.Limit)
	} else if c.Offset > 0 {
		return fmt.Sprintf("offset %d", c.Offset)
	}
	return "limit 0/offset 0"
}

func (c *Limit) Inputs() []*OutputEdge { return []*OutputEdge{c.Input} }
func (c *Limit) Outputs() []*InputEdge { return []*InputEdge{c.Output} }

func (c *Limit) Execute(plan *Plan) error {
	if plan.DryRun {
		c.Output.SetIterator(nil)
		return nil
	}
	return nil
}
