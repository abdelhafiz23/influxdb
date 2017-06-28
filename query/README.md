# Overview

There are two major projects on the horizon with the query engine
related to pure infrastructure. Both of these major projects have many
side benefits (which are being used to justify doing these projects),
but the major reason for doing them is from an infrastructure and
technical debt standpoint.

These two projects are _explain plan_ and _refactor query structure_.

Luckily, these refactors are not actually as significant as they appear.
Most of the necessary work has been laid out over the long period
between 1.2 and 1.3 and has finally culminated in a way where I think it
can be finished by 1.4. After these projects are done, it should be much
easier to implement new features and we can focus on building out more
functions, new optimizations, or a new query language rather than
focusing on internal issues that prevent the above.

This is where you can help rather significantly and I've written this
document to hopefully transmit some of my internal thoughts on the
subject so the overall system is understandable.

The purpose of the _refactor query structure_ is twofold. There's the
external component to allow a method of returning the data structure in
a way that's easier for clients to read iteratively, but it has a side
benefit that could be even more important. Previously, chunking was
strangely handled both within the query engine and within the httpd
service. The `influxql.Emitter` class was used to read and aggregate
rows from series and then these chunks were sent to the httpd service to
be returned to the client through whichever encoding was used.

The change to the query structure is also a change to how the internal
httpd service reads the response too. The refactor changes things so the
query engine returns each series and the rows within a series rather
than batching the points internally and sending them to the httpd
service that way. The MessagePack encoder returns the data structure as
the internal query engine understands the data while the encoders for
JSON and CSV translate this internal format to the old format. The most
important part of this is that it takes the formatting code out of the
query engine and moves it into the httpd service which helps greatly
simplify the emitter.

The purpose of the _explain plan_ is to consolidate the various
different moving parts of the query engine into one place and then allow
the plan to be executed. Here are the various places the query engine
touches currently.

* coordinator.StatementExecutor
* various functions in influxql
* tsdb.Store and tsdb.Shard
* tsm1.Engine
* httpd.Handler

Some handle more than others, but _all_ of these handle some portion of
the query engine and its both confusing and maddening. If things remain
this way, you might as well just get a stuffed rabbit, make some tea,
and start mumbling about how Alice is going to be late to tea time
before you start reading the code. You'll need them soon anyway.

Here's how the current data flow of a query works.

* The httpd service parses the query using `influxql.ParseQuery`.
* `influxql.ParseQuery` validates the syntax tree to see if it is
  executable, but does not create any output from this process. It is a
  boolean process. Either the query validates or it doesn't.
* Execute the query with the query engine. This gets sent to
  `influxql.QueryExecutor`.
* Creates a new entry in the TaskManager.
* Iterates through each statement within the query.
  * Determines the default database.
  * Rewrites the statement (if necessary) for meta queries.
  * Normalize the query to use the default database in sections where it
    is required.
  * Prints the query.
  * Executes the query with the `coordinator.StatementExecutor`.
    * Replace all instances of `now()` with the current time.
    * Find the time range of the select statement.
    * Rewrite `DISTINCT value` calls to `DISTINCT(value)` in the AST (no
      reason this should even be required to begin with).
    * Remove `time` from the list of fields if it is present.
    * Rewrite the time condition to include `time <= now()` if the query
      has a time grouping.
    * Optimize regular expressions like `=~ /^foo$/` to be `= 'foo'`
      instead.
    * Map the sources to the necessary shards for executing the query.
    * Rewrite the fields. This involves rewriting wildcards and regular
      expressions along with finalizing the type for each variable
      reference.
    * Validate the number of buckets that will be created for time
      groupings doesn't exceed the maximum.
    * Send the query to the query engine. (Calls `influxql.Select`).

There's more to the process, but, if you notice, a depressingly small
amount of work is actually done in the "query engine" within the
`influxql` package. It _feels_ like that's where the work should be
getting done, but most of the work isn't. And because validation is
performed as a completely arbitrary step that doesn't do anything, we
have to revalidate the input to `influxql.Select` _during_ execution
rather than at the beginning.

This isn't how it should be. Almost everything, including validation,
should be done in the query engine. That's a primary end goal is to
invalidate most of these functions because they will be unnecessary.

But, that's a large task. Even if it is made easier by previous work
that indirectly aided this effort. Explain plan is a perfect time to
work on fixing this because much of this work _can_ be done while
creating the query plan. The current system cannot do it because of a
bunch of random factors that require some of the query to already have
been processed before passing it to `influxql.Select`.

## Proposed Architecture

One thing to remember is that most of this architecture is already true
either in the case of theory or the case of reality. A lot of the work
is just getting the code to match the theory more directly rather than
the more ad-hoc spread between multiple packages that it currently is.

* Parse a query (validation not required)
* Build a query plan (validate while building the query plan)
* Execute the plan
* Return the rows from the httpd service

The most significant difference is how much goes into building the plan.
Building the plan should encapsulate everything that the query engine
will do so executing the plan can be incredibly dumb.

Validation will be removed from parsing the query and moved to the
compilation step (building the query plan). The reason why we keep
validation around is so we don't create any iterators until we know the
query will at least have a chance of executing without error. Since
iterators are not created during query building, we can combine
validation with the actual construction of the query plan.

The query plan is made up of a directed acyclical graph (DAG). The model
for the executor is based off of the ninja build system with some
modifications. There are a few reasons why this was chosen.

The first is because iterator creation already matches a DAG. Iterators
are produced by one action and then consumed by another iterator until
you get a final iterator that you read from as part of the final
execution.

The second is to allow the executor to be parallelized more easily. If
we want to create 8 iterators at a time, using nodes to encapsulate each
step is a much easier way to separate tasks. Build systems are just
specialized task runners. We have the task of creating iterators.

The third reason for using this architecture is because it avoids
recursion. While not significant, I don't like too much recursion on the
stack when parallelizing operations. This third one is a personal
preference of mine rather than being a requirement, but I think it leads
to cleaner code. I don't really have proof for that one. It's more of a
gut feeling based off of previous work that I've done in previous jobs.

### Build Plan

The DAG is composed of nodes and edges. Each edge has two ends. There's
the InputEdge and the OutputEdge. An InputEdge will always have an
OutputEdge it references. Ends of the edge will be connected to a Node,
which is an interface.

Node is an interface and will be different for every action. A Node can
have multiple inputs or multiple outputs, but the underlying type that
implements Node may only be able to have one output. The implementation
of each Node type should be dead simple. The Limit node type should only
consume an iterator and wrap it in a limit iterator. Executing a node
should require absolutely no thinking. No casting. No anything. The only
time that they should return errors is when something unexpected
happens. It should be very rare to non-existant.

This type of DAG allows us to compile incredibly simple plans and
manipulate them. Since each edge needs to know the node they reference,
splitting the edge into two ends allows us to be able to split a node
without modifying the edge or the nodes the edges point to. That's why
we don't just have the edge be a single class and we split the input end
of an edge from the output edge.

Implementing each Node in this way also allows us to reduce the
possibility of errors. While the Node interface can technically return
multiple inputs, some nodes, like limit, will only take one input and
produce one output. Since the underlying implementation can only
possibly take one input, there's more of a chance a compiler error will
catch the error. This way, we don't have to write something like this in
a lot of nodes:

    if len(inputs) != 1 {
        return fmt.Errorf("invalid number of inputs: %d", len(inputs))
    }

This scenario just won't be possible since it will be impossible to
create a Node type that allows this.

While we have this DAG, we will also be able to manipulate the DAG to
move nodes. So, for example, if we see the following:

    shard [1] -\
    shard [2] ----> merge --> count()
    shard [3] -/

We can optimize the merge. The merge knows it is going to send its
output to a function call and it knows that function call can be
partially executed and merged. So it optimizes things by inserting a new
node between each shard and the merge node and modifies the function
call to be a `sum()` call instead.

    shard [1] --> count() --\
    shard [2] --> count() ----> merge --> sum()
    shard [3] --> count() --/

### Build Executor

The build executor will consume nodes within the graph. It may process
these nodes in parallel. Each node will perform a simple action and
actions may produce additional nodes that are dynamically added to the
plan. The plan should be capable of nodes being added dynamically.

When a node finishes, it checks its outputs to see if all of their
dependencies have been completed. If they have, it now schedules the
outputs.

Ideally, we would have some way to visualize this at some point for
debugging. That way, we could build a JavaScript application that
displays the existing nodes and can highlight which ones are running and
which ones complete. This is just an idea in my head at the moment and
isn't a formal plan.

#### Partial DAG

Might need the ability to execute a partial DAG. Need some method so
additional nodes are not created until other nodes are consumed, but
also need to prevent all of the currently executing nodes from blocking
the executor because they are waiting for other nodes to be executed.

Might not be possible or even needed. The number of series only ends up
mattering for the storage engine and we can just use a different method
for that. But, it's something to consider since we want to reuse some of
the plan execution code in the storage engine.

The reason why I think this might be needed is for TSI. If we have to
create a node in the DAG for each series, then that could easily balloon
out of control. We don't want to create a graph with 10 million nodes
because there are 1 million series with 10 nodes per series. It's also
slightly unnecessary. But we still need to interact with the executor
because we want the creation of each series to be parallel.

Another important section of this might be to handle memory carefully.
Once we have built the inputs of a node and have retrieved the iterators
from these inputs, we might want to consider setting some pointers to
nil so the garbage collector has an easier time collecting memory.

We'll have to be careful about how that's done and likely won't be
included in the first version because it's complicated and likely
unnecessary for overall functioning. But, it's something to keep in mind
since it might influence certain decisions.

## Additional Information

### Query Engine and InfluxQL

The query engine and influxql are different things and they are not
synonyms. InfluxQL gets read and translated into an internal data
structure that is then passed around during iterator creation.

The reason this is difficult is because the query engine is mostly
spread out among 2-4 packages where a significant portion of that is
located inside of the influxql package. Ideally, influxql would be
located in its own package and the query engine would have its own
package too. This explain branch is the work trying to do that (while
also implementing explain and a further refactor).

### Returned Data Structures

An older version of InfluxDB referred to rows in a way that matches more
closely with what we now call series. There are still many sections of
the code that talk about returning a "row" when they are really returning
a series.

Issue #7450 is currently addressing that. While it doesn't remove the
old data structure (due to it being used in other places within
InfluxDB), it uses new data structures that match with how we actually
handle data rather than how we pretend to handle data.

Still, without that PR, you have to know the legacy data structure and
how it is handled.

## Subsystems and Data Flow

When I refer to sections of the query engine, I use three distinct
sections. After I finish working on `EXPLAIN`, these sections should be
distinct and much easier to understand. At the moment, they are spread
out between approximately 4 packages.

There is the _query language_. The query language comprises the frontend
and is usually what people mistake for the query engine. The language
exists because there needs to be some user-facing way to interact with
the query engine. Its significance both begins and ends with that. With
proper separation of the query language from the query engine, it is
fully possible to substitute any query language on top of the query
engine.

While the query engine does not execute queries off of the query
language, some data structures are shared between the two. In
particular, the `influxql.Call` and `influxql.VarRef` are used and
passed around within the `influxql.IteratorOptions` structure to create
certain types of queries. This is not ideal and implementing a proper
query plan is meant to remove this disparity. When we're done with the
query plan, there should be no references to `influxql` data structures
within the query engine outside of query compilation.

There is the _query engine_. This is the subsystem that creates and
processes iterators. A bulk of query execution is in here, but some
components are kept inside of the `tsdb`, `coordinator`, and `tsm1`
packages. Once the query plan is implemented, the sections of code that
are kept in those packages (mostly optimization related code) will all
be in the `query` package and only the things related to the _storage
engine_ will be in any of these packages.

The purpose of the code in the _storage engine_ is providing a way to
access the data. The query engine asks the storage engine to create an
iterator with certain options and it is the storage engine's job to
create the low level cursors that access the data and wrap them in
iterators. Ideally, the storage engine would do nothing except for
exactly what the query engine tells it to do. At the moment, the storage
engine performs some unsolicited optimizations when accessing certain
data. One purpose of the query plan is to make the storage engine dumber
and the query engine smarter so less code can be in the storage engine.

One example is the `first()` and `last()` optimizations. When the query
engine asks for the `first()` or `last()` without a time grouping, the
storage engine quietly changes this to doing a `LIMIT 1` query in either
ascending or descending order. This optimization should instead be done
during query planning so it can be visualized with `EXPLAIN` and the
storage engine should be asked by the query engine to do `LIMIT 1`
instead of the storage engine doing this optimization unsolicited. This
kind of separation of concerns will allow much less code to exist in the
storage engine so query work can be confined mostly to the `query`
package.

## Implementation Steps

* Construct the Edge structs and some sample Nodes. Ensure this kind of
  structure actually works.
  * The original structure only had an Edge struct and did not separate
    these into two. This was found to make it difficult to insert new
    nodes between other edges in practice which is why the edges are now
    split into two structs. A side benefit of this is it's harder to
    confuse if you are reading or writing since everything is named
    "input" and "output" as you read from output edges within nodes but
    the output edge is called that because it's the end of the edge even
    if the node reads from it.
* Add descriptions to each of the nodes.
* Modify the AST to include an explain command.
* Modify the coordinator to build a query plan and execute it in "dry
  run" mode. Have it print out the results in a format. The format
  didn't really matter and is completely negotiable.
* Modify the coordinator to use the same query plan to build the query.
* Support some basic auxiliary iterators in explain plan.
* Support a basic call iterator such as count() in explain plan.
* Implement the partial aggregate optimization in the explain plan by
  modifying the AST.
* Actually implement all of the above.
* Find a way to send a plan node to the storage engine for execution.
* Experiment with sub plans so a plan can be optimized and a portion of
  the plan can be sent to remote node (the storage engine) to be
  executed.
* Begin to integrate with plutonium by transferring marshaling the query
  plan across the wire.
* Work on plan compilation for everything in influxql.Select.

## Remote Execution

We will need some way to execute nodes remotely. The problem is that we
have to instantiate them as part of the graph. Or at least we want to
construct the graph and figure out which parts can be executed remotely.
It seems like it might be part of the Node interface. Essentially, we
need a way to run a node and have it absorb some of its dependent nodes
and remove them from the run graph. This should be possible by changing
the outputs of the currently running node.

We can likely leave the remote creator as a black box that just says
"iterators will be created remotely" and then have that iterator
creating remotely perform its own optimizations by pulling in outputs.

So in the graph, we see each shard creator get told to perform "count()"
afterwards.

One thing we might want to see is some form of versioning. This might
have to be optional, but it would be nice to be able to push the graph
to the remote node and have the remote node be able to say, "I don't
know how to handle that." The format would either be by sending the
nodes over as a bread-first search or depth-first search.

So something like sending the node definition and then sending the
node's outputs (along with the number of outputs).

Whenever a node is read successfully, the server would send back a
response telling the server to send more. If the server returns an
error, the node is not sent over and is instead handled locally. If any
of the origin nodes are not accepted (those without inputs), then the
series fails. When nodes are finished being sent, a message has to be
sent (or maybe just an EOF? but that's worked badly for us in the past).

Maybe we would send the message as blobs.

Send the origin nodes first (that way, if any are not accepted, we do
not have to do much). If we do it that way, that indicates we would need
to do a breadth-first search. This might be harder since there is more
memory that we would have to keep (to know who we are referencing), but
we might just be able to keep pointers to the memory. So we create the
slice, take the memory of that slice, and then put it into a linked list
since that will be more memory efficient as a queue.

We would need to define a serialization format for each of the structs.

```
message Node {
    required string Name = 1;
    repeated Argument Args = 2;
}

message Argument {
    required string Key = 1;
    optional bytes Value = 2;
}

message Response {
    optional string Error = 1;
}
```

The above would define how to serialize a node. The name would tell the
remote service which struct was being used. Structs need to be
registered to be instantiated to avoid a remote execution vulnerability.

The first thing we should do is test serializing a single node and
having it be instantiated and returning an iterator.

Another difficulty is we need to make sure only one iterator is actually
produced (although I guess we can add an implicit merge at the end or
just return them in serial).

After each node transfer, the client would read the response that is
marshaled back. If there is a fatal error (such as an 

## Unknown Number of Outputs

Interestingly, we want compile to work even when we don't know the
number of outputs.

Previously, we had this reading from the output edges, but that makes it
hard to add additional output nodes dynamically (such as from a
wildcard). It might be more productive to have a special node endpoint
that has an execute that does nothing, has no outputs, but can be
referenced to retrieve iterators from.

So the node references the endpoint. Those endpoints are what gets
returned.
