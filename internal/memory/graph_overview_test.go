package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type graphFixture struct {
	t     *testing.T
	store *SQLiteStore
	ctx   context.Context
}

func newGraphFixture(t *testing.T) *graphFixture {
	t.Helper()
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DB().Close() })
	return &graphFixture{t: t, store: store, ctx: context.Background()}
}

func (f *graphFixture) add(id, scope, modality string, meta map[string]interface{}) {
	f.t.Helper()
	r := Record{ID: id, Scope: scope, Modality: modality, Status: "active", Content: "content of " + id, Metadata: meta}
	if err := f.store.Ingest(f.ctx, r); err != nil {
		f.t.Fatal(err)
	}
}

func (f *graphFixture) link(a, b string) {
	f.t.Helper()
	if err := f.store.Link(f.ctx, a, b, "related"); err != nil {
		f.t.Fatal(err)
	}
}

func (f *graphFixture) setStatus(id, status string) {
	f.t.Helper()
	if _, err := f.store.db.Exec(`UPDATE memories SET status=? WHERE id=?`, status, id); err != nil {
		f.t.Fatal(err)
	}
}

func (f *graphFixture) graph(req GraphRequest) GraphData {
	f.t.Helper()
	g, err := f.store.Graph(f.ctx, req)
	if err != nil {
		f.t.Fatal(err)
	}
	return g
}

func graphIDs(g GraphData) string {
	out := make([]string, len(g.Nodes))
	for i, n := range g.Nodes {
		out[i] = n.ID
	}
	return strings.Join(out, ",")
}

// hub is linked to a, b, c; a is also linked to b.
func (f *graphFixture) star() {
	for _, id := range []string{"hub", "a", "b", "c"} {
		f.add(id, "global", "fact", nil)
	}
	f.link("hub", "a")
	f.link("hub", "b")
	f.link("hub", "c")
	f.link("a", "b")
}

func TestOverviewRanksByNumberOfLinks(t *testing.T) {
	f := newGraphFixture(t)
	f.star()
	g := f.graph(GraphRequest{})
	if g.Nodes[0].ID != "hub" || g.Nodes[0].Degree != 3 {
		t.Fatalf("most connected memory not first: %+v", g.Nodes)
	}
	if len(g.Nodes) != 4 || len(g.Edges) != 4 || g.Meta.Total != 4 || g.Meta.Truncated {
		t.Fatalf("whole graph expected: nodes=%d edges=%d meta=%+v", len(g.Nodes), len(g.Edges), g.Meta)
	}
	if g.Nodes[1].ID != "a" && g.Nodes[1].ID != "b" {
		t.Fatalf("second place should be one of the two with 2 links, got %s", g.Nodes[1].ID)
	}
}

func TestOverviewLimitKeepsTheMostLinkedAndSaysItTruncated(t *testing.T) {
	f := newGraphFixture(t)
	f.star()
	g := f.graph(GraphRequest{Limit: 2})
	if len(g.Nodes) != 2 || !g.Meta.Truncated || g.Meta.Total != 4 || g.Meta.Returned != 2 {
		t.Fatalf("limit not applied honestly: %s %+v", graphIDs(g), g.Meta)
	}
	for _, e := range g.Edges {
		in := map[string]bool{g.Nodes[0].ID: true, g.Nodes[1].ID: true}
		if !in[e.Source] || !in[e.Target] {
			t.Fatalf("edge to a node that was cut off: %+v", e)
		}
	}
	// A returned node still reports all its links, so the drawing can say "more not shown".
	if g.Nodes[0].ID == "hub" && g.Nodes[0].Degree != 3 {
		t.Fatalf("degree must count links to nodes that were cut: %+v", g.Nodes[0])
	}
}

func TestOverviewOrderIsStableWhenLinkCountsTie(t *testing.T) {
	f := newGraphFixture(t)
	for _, id := range []string{"m3", "m1", "m2", "m4"} {
		f.add(id, "global", "fact", nil)
	}
	f.link("m1", "m2")
	f.link("m3", "m4")
	first := graphIDs(f.graph(GraphRequest{}))
	for i := 0; i < 5; i++ {
		if got := graphIDs(f.graph(GraphRequest{})); got != first {
			t.Fatalf("order changed between calls: %s vs %s", got, first)
		}
	}
	if first != "m1,m2,m3,m4" {
		t.Fatalf("ties should fall back to id order, got %s", first)
	}
}

func TestOverviewCountsMemoriesThatHaveNoLinks(t *testing.T) {
	f := newGraphFixture(t)
	f.star()
	f.add("lonely1", "global", "fact", nil)
	f.add("lonely2", "global", "episode", nil)
	g := f.graph(GraphRequest{})
	if g.Meta.Isolated != 2 || g.Meta.Total != 4 {
		t.Fatalf("isolated=%d total=%d", g.Meta.Isolated, g.Meta.Total)
	}
	g = f.graph(GraphRequest{Modalities: []string{"fact"}})
	if g.Meta.Isolated != 1 {
		t.Fatalf("isolated memories of other kinds must not be counted: %d", g.Meta.Isolated)
	}
}

func TestGraphShowsGlobalAndTheNamedConversationOnly(t *testing.T) {
	f := newGraphFixture(t)
	f.add("g1", "global", "fact", nil)
	f.add("mine", "conv-1", "fact", nil)
	f.add("theirs", "conv-2", "fact", nil)
	f.link("g1", "mine")
	f.link("g1", "theirs")
	f.link("mine", "theirs")

	if got := graphIDs(f.graph(GraphRequest{Scope: "conv-1"})); got != "g1,mine" {
		t.Fatalf("conv-1 sees %q", got)
	}
	g := f.graph(GraphRequest{Scope: "global"})
	if len(g.Nodes) != 0 {
		t.Fatalf("global view must not include conversation memories: %s", graphIDs(g))
	}
	g = f.graph(GraphRequest{Scope: "conv-1"})
	if g.Nodes[0].Degree != 1 {
		t.Fatalf("a link to a hidden memory leaked into the degree: %+v", g.Nodes)
	}
	if _, err := f.store.Graph(f.ctx, GraphRequest{Mode: GraphModeEgo, Center: "theirs", Scope: "conv-1"}); !errors.Is(err, ErrGraphNotFound) {
		t.Fatalf("another conversation's memory must not be a centre: %v", err)
	}
}

func TestGraphLeavesOutMemoriesThatAreNotActiveAndPersonalEvidence(t *testing.T) {
	f := newGraphFixture(t)
	for _, id := range []string{"keep1", "keep2", "gone", "arch", "evid"} {
		meta := map[string]interface{}(nil)
		if id == "evid" {
			meta = map[string]interface{}{"source": "personal_learning_evidence"}
		}
		f.add(id, "global", "fact", meta)
	}
	f.link("keep1", "keep2")
	f.link("keep1", "gone")
	f.link("keep1", "arch")
	f.link("keep1", "evid")
	f.setStatus("gone", "forgotten")
	f.setStatus("arch", "archived")
	g := f.graph(GraphRequest{})
	if graphIDs(g) != "keep1,keep2" || g.Nodes[0].Degree != 1 {
		t.Fatalf("hidden memories showed up: %s %+v", graphIDs(g), g.Nodes)
	}
}

func TestGraphIgnoresALinkToItself(t *testing.T) {
	f := newGraphFixture(t)
	f.add("solo", "global", "fact", nil)
	f.link("solo", "solo")
	if g := f.graph(GraphRequest{}); len(g.Nodes) != 0 || g.Meta.Isolated != 1 {
		t.Fatalf("self link made a node: %s %+v", graphIDs(g), g.Meta)
	}
}

func TestKindFilterAppliesToBothEndsButNotToTheCentre(t *testing.T) {
	f := newGraphFixture(t)
	f.add("f1", "global", "fact", nil)
	f.add("f2", "global", "fact", nil)
	f.add("e1", "global", "episode", nil)
	f.link("f1", "f2")
	f.link("f1", "e1")

	g := f.graph(GraphRequest{Modalities: []string{"fact"}})
	if graphIDs(g) != "f1,f2" || len(g.Edges) != 1 || g.Nodes[0].Degree != 1 {
		t.Fatalf("filter not applied to both ends: %s %+v", graphIDs(g), g.Edges)
	}
	// The centre is shown whatever its kind; only its neighbours are filtered.
	g = f.graph(GraphRequest{Mode: GraphModeEgo, Center: "e1", Modalities: []string{"fact"}})
	if graphIDs(g) != "e1,f1" {
		t.Fatalf("an episode centre with a fact filter should show itself and its fact neighbour: %s", graphIDs(g))
	}
	g = f.graph(GraphRequest{Mode: GraphModeEgo, Center: "f2", Modalities: []string{"episode"}})
	if graphIDs(g) != "f2" {
		t.Fatalf("a fact centre with an episode filter has no episode neighbour, so it is alone: %s", graphIDs(g))
	}
	g = f.graph(GraphRequest{Mode: GraphModeEgo, Center: "f1", Modalities: []string{"Fact"}})
	if graphIDs(g) != "f1,f2" {
		t.Fatalf("filter should ignore case and drop the episode: %s", graphIDs(g))
	}
	if g.Meta.Groups["fact"] != 2 || g.Meta.Groups["episode"] != 0 {
		t.Fatalf("group counts: %+v", g.Meta.Groups)
	}
}

// chain: c0 - c1 - c2 - c3 - c4
func (f *graphFixture) chain(n int) {
	for i := 0; i < n; i++ {
		f.add(fmt.Sprintf("c%d", i), "global", "fact", nil)
	}
	for i := 0; i+1 < n; i++ {
		f.link(fmt.Sprintf("c%d", i), fmt.Sprintf("c%d", i+1))
	}
}

func TestEgoWalksOutwardByDepth(t *testing.T) {
	f := newGraphFixture(t)
	f.chain(5)
	for depth, want := range map[int]string{0: "c2,c1,c3", 1: "c2,c1,c3", 2: "c2,c1,c3,c0,c4", 3: "c2,c1,c3,c0,c4"} {
		g := f.graph(GraphRequest{Mode: GraphModeEgo, Center: "c2", Depth: depth})
		got := graphIDs(g)
		// order within a level may vary with ties, so compare as sets with the centre first
		if strings.Split(got, ",")[0] != "c2" || len(g.Nodes) != len(strings.Split(want, ",")) {
			t.Fatalf("depth %d: got %s want %s", depth, got, want)
		}
		wantSet := map[string]bool{}
		for _, w := range strings.Split(want, ",") {
			wantSet[w] = true
		}
		for _, n := range g.Nodes {
			if !wantSet[n.ID] {
				t.Fatalf("depth %d: unexpected %s in %s", depth, n.ID, got)
			}
		}
		if g.Meta.Center != "c2" || g.Meta.Truncated {
			t.Fatalf("depth %d meta: %+v", depth, g.Meta)
		}
	}
}

func TestEgoDepthIsCappedAtThree(t *testing.T) {
	f := newGraphFixture(t)
	f.chain(8)
	g := f.graph(GraphRequest{Mode: GraphModeEgo, Center: "c0", Depth: 50})
	if len(g.Nodes) != 4 || g.Meta.Depth != 3 {
		t.Fatalf("depth cap: %s %+v", graphIDs(g), g.Meta)
	}
}

func TestEgoLimitStopsTheWalkAndReportsTruncation(t *testing.T) {
	f := newGraphFixture(t)
	f.add("hub", "global", "fact", nil)
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("leaf%d", i)
		f.add(id, "global", "fact", nil)
		f.link("hub", id)
	}
	g := f.graph(GraphRequest{Mode: GraphModeEgo, Center: "hub", Limit: 3})
	if len(g.Nodes) != 3 || g.Nodes[0].ID != "hub" || !g.Meta.Truncated || g.Meta.Total != 7 {
		t.Fatalf("limit: %s %+v", graphIDs(g), g.Meta)
	}
	g = f.graph(GraphRequest{Mode: GraphModeEgo, Center: "hub", Limit: 7})
	if len(g.Nodes) != 7 || g.Meta.Truncated {
		t.Fatalf("exactly at the limit is not truncated: %s %+v", graphIDs(g), g.Meta)
	}
}

func TestEgoPrefersTheStrongerLink(t *testing.T) {
	f := newGraphFixture(t)
	for _, id := range []string{"x", "weak", "strong"} {
		f.add(id, "global", "fact", nil)
	}
	f.link("x", "weak")
	f.link("x", "strong")
	if _, err := f.store.db.Exec(`UPDATE memory_links SET weight=5 WHERE target_id='strong'`); err != nil {
		t.Fatal(err)
	}
	g := f.graph(GraphRequest{Mode: GraphModeEgo, Center: "x", Limit: 2})
	if graphIDs(g) != "x,strong" {
		t.Fatalf("expected the stronger link to win a scarce slot: %s", graphIDs(g))
	}
}

// A memory reached through two paths is ranked by the stronger one.
func TestEgoRanksByTheStrongestPathToAMemory(t *testing.T) {
	f := newGraphFixture(t)
	for _, id := range []string{"x", "a", "b", "t", "u"} {
		f.add(id, "global", "fact", nil)
	}
	f.link("x", "a")
	f.link("x", "b")
	f.link("a", "t") // t is reached from a with weight 1 ...
	f.link("b", "t") // ... and from b with weight 5
	f.link("a", "u") // u is reached only from a, with weight 3
	if _, err := f.store.db.Exec(`UPDATE memory_links SET weight=5 WHERE source_id='b' AND target_id='t'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`UPDATE memory_links SET weight=3 WHERE source_id='a' AND target_id='u'`); err != nil {
		t.Fatal(err)
	}
	g := f.graph(GraphRequest{Mode: GraphModeEgo, Center: "x", Depth: 2, Limit: 4})
	if !strings.Contains(graphIDs(g), "t") || strings.Contains(graphIDs(g), "u") {
		t.Fatalf("the memory with the stronger path (t, 5) should win the last slot over u (3): %s", graphIDs(g))
	}
}

func TestEgoOfAMemoryWithoutLinksIsJustThatMemory(t *testing.T) {
	f := newGraphFixture(t)
	f.add("alone", "global", "fact", nil)
	g := f.graph(GraphRequest{Mode: GraphModeEgo, Center: "alone"})
	if graphIDs(g) != "alone" || len(g.Edges) != 0 || g.Meta.Truncated {
		t.Fatalf("%s %+v", graphIDs(g), g.Meta)
	}
}

func TestEgoNeedsAVisibleCentre(t *testing.T) {
	f := newGraphFixture(t)
	f.add("m", "global", "fact", nil)
	f.setStatus("m", "forgotten")
	for _, id := range []string{"missing", "m"} {
		if _, err := f.store.Graph(f.ctx, GraphRequest{Mode: GraphModeEgo, Center: id}); !errors.Is(err, ErrGraphNotFound) {
			t.Fatalf("centre %q: %v", id, err)
		}
	}
	if _, err := f.store.Graph(f.ctx, GraphRequest{Mode: GraphModeEgo}); err == nil {
		t.Fatal("ego without a centre was accepted")
	}
	if _, err := f.store.Graph(f.ctx, GraphRequest{Mode: "sideways"}); err == nil {
		t.Fatal("unknown mode was accepted")
	}
}

func TestGraphLimitIsClamped(t *testing.T) {
	f := newGraphFixture(t)
	f.star()
	if g := f.graph(GraphRequest{Limit: -5}); len(g.Nodes) != 1 {
		t.Fatalf("a negative limit should clamp to 1: %d", len(g.Nodes))
	}
	if g := f.graph(GraphRequest{Limit: 1_000_000}); len(g.Nodes) != 4 {
		t.Fatalf("a huge limit is capped, not an error: %d", len(g.Nodes))
	}
}

func TestGraphTitleUsesTopicThenFirstLineAndClipsByCharacters(t *testing.T) {
	long := strings.Repeat("长", 80)
	cases := []struct {
		name string
		r    Record
		want string
	}{
		{"topic", Record{Content: "ignored", Metadata: map[string]interface{}{"topic": " 部署规范 "}}, "部署规范"},
		{"title", Record{Content: "ignored", Metadata: map[string]interface{}{"title": "T"}}, "T"},
		{"first line", Record{Content: "first\nsecond"}, "first"},
		{"clip", Record{Content: long}, strings.Repeat("长", graphTitleRunes-1) + "…"},
	}
	for _, c := range cases {
		if got := graphTitle(c.r); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if got := graphTitle(Record{Content: long}); strings.ContainsRune(got, '\uFFFD') {
		t.Errorf("clipping split a character: %q", got)
	}
}
