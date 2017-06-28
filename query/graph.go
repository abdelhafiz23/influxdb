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
		panic(fmt.Sprintf("attempted to retrieve an iterator from an edge before it was ready: %T", e.Input.Node))
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

// Insert splits the current edge and inserts a Node into the middle.
// It then returns the newly created OutputEdge that points to the inserted
// Node and the newly created InputEdge that the Node should use to send its
// results.
func (e *OutputEdge) Insert(n Node) (*OutputEdge, *InputEdge) {
	// Create a new OutputEdge. The input should be the current InputEdge for
	// this node.
	out := &OutputEdge{Node: n, Input: e.Input}
	// Reset the Input so it points to the newly created output as its input.
	e.Input.Output = out
	// Redirect this OutputEdge's input to a new input edge.
	e.Input = &InputEdge{Node: n, Output: e}
	// Return the newly created edges so they can be stored with the newly
	// inserted Node.
	return out, e.Input
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

type OptimizableNode interface {
	Node
	Optimize()
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

var _ Node = &Iterator{}

// Iterator holds the final Iterator or Iterators produced for consumption.
// It has no outputs and may contain multiple (ordered) inputs.
type Iterator struct {
	Field      *influxql.Field
	InputEdges []*OutputEdge
}

func (i *Iterator) Description() string {
	return i.Field.String()
}

func (i *Iterator) Inputs() []*OutputEdge    { return i.InputEdges }
func (i *Iterator) Outputs() []*InputEdge    { return nil }
func (i *Iterator) Execute(plan *Plan) error { return nil }

func (i *Iterator) Iterators() []influxql.Iterator {
	itrs := make([]influxql.Iterator, 0, len(i.InputEdges))
	for _, input := range i.InputEdges {
		itrs = append(itrs, input.Iterator())
	}
	return itrs
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
	merge.Optimize()
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

func (m *Merge) Optimize() {
	// Nothing to optimize if we are not pointed at anything.
	if m.Output.Output.Node == nil {
		return
	}

	switch node := m.Output.Output.Node.(type) {
	case *FunctionCall:
		// If our output node is a function, check if it is one of the ones we can
		// do as a partial aggregate.
		switch node.Name {
		case "min", "max", "sum", "first", "last", "mean", "count":
			// Pass through.
		default:
			return
		}

		// Create a new function call and insert it at the end of every
		// input to the merge node.
		for _, input := range m.InputNodes {
			call := &FunctionCall{Name: node.Name}
			call.Input, call.Output = input.Insert(call)
		}

		// If the function call was count(), modify it so it is now sum().
		if node.Name == "count" {
			node.Name = "sum"
		}
	}
}

var _ Node = &FunctionCall{}

type FunctionCall struct {
	Name   string
	Arg    influxql.VarRef
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

	call := &influxql.Call{
		Name: c.Name,
		Args: []influxql.Expr{&c.Arg},
	}
	opt := influxql.IteratorOptions{
		Expr:      call,
		StartTime: influxql.MinTime,
		EndTime:   influxql.MaxTime,
	}
	itr, err := influxql.NewCallIterator(c.Input.Iterator(), opt)
	if err != nil {
		return err
	}
	c.Output.SetIterator(itr)
	return nil
}

type Distinct struct{}

type AuxiliaryFields struct {
	Aux     []influxql.VarRef
	Input   *OutputEdge
	Output  *InputEdge
	outputs []*InputEdge
	refs    []*influxql.VarRef
}

func (c *AuxiliaryFields) Description() string {
	return "access auxiliary fields"
}

func (c *AuxiliaryFields) Inputs() []*OutputEdge { return []*OutputEdge{c.Input} }
func (c *AuxiliaryFields) Outputs() []*InputEdge {
	if c.Output != nil {
		outputs := make([]*InputEdge, 0, len(c.outputs)+1)
		outputs = append(outputs, c.Output)
		outputs = append(outputs, c.outputs...)
		return outputs
	} else {
		return c.outputs
	}
}

func (c *AuxiliaryFields) Execute(plan *Plan) error {
	if plan.DryRun {
		if c.Output != nil {
			c.Output.SetIterator(nil)
		}
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
	if c.Output != nil {
		c.Output.SetIterator(aitr)
		aitr.Start()
	} else {
		aitr.Background()
	}
	return nil
}

// Iterator registers a new OutputEdge that registers the VarRef to be read and
// sent to the OutputEdge.
func (c *AuxiliaryFields) Iterator(ref *influxql.VarRef) *OutputEdge {
	// Create the new edge.
	in, out := NewEdge(c)
	c.outputs = append(c.outputs, in)

	// Attempt to find an existing variable that matches this one to avoid
	// duplicating the same variable reference in the auxiliary fields.
	for idx := range c.Aux {
		v := &c.Aux[idx]
		if *v == *ref {
			c.refs = append(c.refs, v)
			return out
		}
	}

	// Register a new auxiliary field and take a reference to it.
	c.Aux = append(c.Aux, *ref)
	c.refs = append(c.refs, &c.Aux[len(c.Aux)-1])
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

	opt := influxql.IteratorOptions{}
	lhs, rhs := c.LHS.Iterator(), c.RHS.Iterator()
	itr, err := influxql.BuildTransformIterator(lhs, rhs, c.Op, opt)
	if err != nil {
		return err
	}
	c.Output.SetIterator(itr)
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
