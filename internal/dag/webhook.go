package dag

import "fmt"

// webhookPluginName is the source plugin whose entries arrive by push rather
// than by polling. Its "queue" config names the ingest queue it drains, which
// is the {queue} path segment pushers POST to.
const webhookPluginName = "webhook"

// WebhookQueues lists the ingest queues drained by a graph's webhook sources,
// in node order. Callers use it two ways: the dashboard marks push-fed
// pipelines and counts waiting items, and the ingest endpoint resolves a
// pipeline name to the queue a push should land in — so a caller naming the
// pipeline need not also know the queue, which is a property of the pipeline
// rather than independent information.
//
// A graph with no webhook source returns nil, which is how the endpoint tells
// "this pipeline cannot be pushed to" from "this queue is the wrong one".
func WebhookQueues(g *Graph) []string {
	if g == nil {
		return nil
	}
	var queues []string
	for _, n := range g.Nodes() {
		if n.PluginName != webhookPluginName {
			continue
		}
		if q, _ := n.Config["queue"].(string); q != "" {
			queues = append(queues, q)
		}
	}
	return queues
}

// webhookWarnings flags a pipeline with more than one webhook source. That is
// a legal topology — merge and route exist to combine sources, and two queues
// feeding one pipeline is a reasonable thing to want — but it makes the queue
// no longer derivable from the pipeline name, so a push that names only the
// pipeline is ambiguous and will be refused. Saying so at config load beats
// finding out from a 400 on the first request.
func webhookWarnings(g *Graph) []error {
	qs := WebhookQueues(g)
	if len(qs) < 2 {
		return nil
	}
	return []error{fmt.Errorf(
		"pipeline has %d webhook sources (queues %v), so a push cannot derive the queue from the pipeline name alone — such a request must name the queue explicitly",
		len(qs), qs)}
}
