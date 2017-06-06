package query_test

import (
	"fmt"
	"testing"

	"github.com/influxdata/influxdb/query"
)

func TestNode(t *testing.T) {
	shards := make([]*query.Node, 0, 3)
	for i := 0; i < 3; i++ {
		sh := &query.Node{
			Name: fmt.Sprintf("create shard %d", i),
		}
		shards = append(shards, sh)
	}
	merge := &query.Node{Name: "merge shards"}
	count := &query.Node{Name: "count()"}
	itr := &query.Node{Name: "iterator"}

	for _, sh := range shards {
		edge := &query.Edge{
			Source: sh,
			Target: merge,
		}
		sh.Outputs = append(sh.Outputs, edge)
		merge.Inputs = append(merge.Inputs, edge)
	}

	merge.Outputs = []*query.Edge{{Source: merge, Target: count}}
	count.Inputs = merge.Outputs

	count.Outputs = []*query.Edge{{Source: count, Target: itr}}
	itr.Inputs = count.Outputs

	root := query.FindRootNodes([]*query.Node{itr})
	for _, n := range root {
		fmt.Println(n)
	}

	nodes, err := query.TopologicalSort([]*query.Node{itr})
	if err != nil {
		t.Error(err)
	}

	for _, n := range nodes {
		fmt.Println(n.Name)
	}
}
