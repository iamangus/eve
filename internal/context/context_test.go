package context

import (
	"strings"
	"testing"
	"time"

	"github.com/iamangus/eve/internal/store"
)

func TestEstimateTokens(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"a", 1},
		{"hello world", 3},
		{"This is a sentence with enough length.", 10},
	}
	for _, c := range cases {
		if got := EstimateTokens(c.in); got != c.want {
			t.Errorf("EstimateTokens(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func comp(id string, createdAt time.Time, importance int, p1, p2, p3, p4 string) store.Compartment {
	return store.Compartment{
		ID:         id,
		CreatedAt:  createdAt,
		Importance: importance,
		Tiers:      store.CompartmentTiers{P1: p1, P2: p2, P3: p3, P4: p4},
	}
}

func TestRenderCompartments_DemotesOldestFirst(t *testing.T) {
	base := time.Now()
	old := comp("old", base.Add(-48*time.Hour), 90, strings.Repeat("x", 400), strings.Repeat("x", 200), strings.Repeat("x", 80), strings.Repeat("x", 20))
	young := comp("young", base, 10, strings.Repeat("y", 400), strings.Repeat("y", 200), strings.Repeat("y", 80), strings.Repeat("y", 20))

	// Target fits only the young at p1 plus a sliver of old at p4.
	target := EstimateTokens(young.Tiers.P1) + EstimateTokens(old.Tiers.P4)
	rendered := renderCompartments([]store.Compartment{old, young}, target)

	if len(rendered) != 2 {
		t.Fatalf("expected both compartments rendered, got %d", len(rendered))
	}
	// Oldest must be demoted before the young one.
	if rendered[0].tier <= rendered[1].tier {
		t.Errorf("oldest compartment should be demoted more: got tier old=%d young=%d", rendered[0].tier, rendered[1].tier)
	}
}

func TestRenderCompartments_DropsWhenTinyTarget(t *testing.T) {
	c := comp("c", time.Now(), 50, strings.Repeat("x", 1000), strings.Repeat("x", 500), strings.Repeat("x", 100), "anchor")
	rendered := renderCompartments([]store.Compartment{c}, 1)
	if len(rendered) != 0 {
		t.Errorf("expected compartment dropped at tiny target, got %d rendered", len(rendered))
	}
}

func TestRenderCompartments_AnchorFits(t *testing.T) {
	c := comp("c", time.Now(), 50, strings.Repeat("x", 1000), strings.Repeat("x", 500), strings.Repeat("x", 100), "anchor")
	rendered := renderCompartments([]store.Compartment{c}, 10)
	if len(rendered) != 1 || rendered[0].tier != 4 {
		t.Errorf("expected compartment rendered at p4, got %+v", rendered)
	}
}

func TestParseManifest_Valid(t *testing.T) {
	text := `{"compartments":[{"importance":75,"p1":"long","p2":"mid","p3":"short","p4":"anchor","facts":[{"category":"decisions","content":"use postgres"}]}]}`
	m, err := ParseManifest(text)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(m.Compartments) != 1 || m.Compartments[0].Importance != 75 {
		t.Fatalf("unexpected manifest: %+v", m.Compartments)
	}
}

func TestParseManifest_Fenced(t *testing.T) {
	text := "Here is your summary:\n```json\n{\"compartments\":[{\"importance\":40,\"p1\":\"a\",\"p2\":\"b\",\"p3\":\"c\",\"p4\":\"d\",\"facts\":[]}]}\n```"
	m, err := ParseManifest(text)
	if err != nil {
		t.Fatalf("parse fenced: %v", err)
	}
	if len(m.Compartments) != 1 {
		t.Fatalf("expected 1 compartment, got %d", len(m.Compartments))
	}
}

func TestParseManifest_FailClosed(t *testing.T) {
	for _, text := range []string{
		"not json at all",
		`{"compartments":[]}`,
		`{"compartments":[{"importance":5,"p1":"","p2":"","p3":"","p4":""}]}`,
	} {
		if _, err := ParseManifest(text); err == nil {
			t.Errorf("expected error for %q", text)
		}
	}
}

func TestCanonicalCategory(t *testing.T) {
	cases := map[string]string{
		"user_preferences": "USER_PREFERENCES",
		"Decisions":        "DECISIONS",
		"weird":            "FACTS",
		"":                 "FACTS",
	}
	for in, want := range cases {
		if got := CanonicalCategory(in); got != want {
			t.Errorf("CanonicalCategory(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMemoryBlock_TopByImportance(t *testing.T) {
	mems := []store.Memory{
		{Content: "low", Importance: 10, CreatedAt: time.Now()},
		{Content: "high", Importance: 90, CreatedAt: time.Now()},
	}
	block := memoryBlock(mems, 1)
	if !strings.Contains(block, "high") || strings.Contains(block, "low") {
		t.Errorf("memoryBlock should include only the highest importance memory, got: %q", block)
	}
}

func TestMemoryBlock_Empty(t *testing.T) {
	if block := memoryBlock(nil, 8); block != "" {
		t.Errorf("expected empty block, got %q", block)
	}
}

func TestRecallMemories_KeywordMatch(t *testing.T) {
	mems := []store.Memory{
		{Content: "user prefers dark mode in the interface", Importance: 80},
		{Content: "the deployment uses kubernetes", Importance: 90},
	}
	got := RecallMemories(mems, "dark mode interface", 5)
	if len(got) != 1 || !strings.Contains(got[0].Content, "dark mode") {
		t.Errorf("expected dark mode memory recalled, got %+v", got)
	}
}

func TestIsNearDuplicate(t *testing.T) {
	if !isNearDuplicate("user likes coffee", "the user likes coffee a lot") {
		t.Error("expected substring relation to be duplicate")
	}
	if isNearDuplicate("user likes coffee", "weather is rainy today") {
		t.Error("expected unrelated memories to not be duplicates")
	}
}

func TestFactHash_Dedup(t *testing.T) {
	h1 := factHash("FACTS", "postgres is the target db")
	h2 := factHash("FACTS", "postgres is the target db")
	h3 := factHash("FACTS", "sqlite is the target db")
	if h1 != h2 || h1 == h3 {
		t.Errorf("factHash dedup broken: %s %s %s", h1, h2, h3)
	}
}

func TestClampImportance(t *testing.T) {
	if clampImportance(-5) != 0 || clampImportance(500) != 100 || clampImportance(42) != 42 {
		t.Error("clampImportance out of range")
	}
}

func TestStorePersistence_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	conv, err := st.CreateConversation("test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := st.AppendUserMessage(conv.ID, "hello"); err != nil {
		t.Fatalf("append: %v", err)
	}
	comp := store.Compartment{
		StartMsgID: 1,
		EndMsgID:   1,
		Importance: 80,
		Tiers:      store.CompartmentTiers{P1: "summary", P2: "sum", P3: "s", P4: "s"},
		Facts:      []store.Fact{{Category: "FACTS", Content: "durable fact"}},
	}
	stored, err := st.AddCompartment(conv.ID, comp)
	if err != nil {
		t.Fatalf("add comp: %v", err)
	}
	if err := st.AddMemory(store.Memory{
		Category:          "FACTS",
		Content:           "durable fact",
		Importance:        80,
		SourceCompartment: stored.ID,
		Hash:              "h1",
	}); err != nil {
		t.Fatalf("add memory: %v", err)
	}

	// Reopen from disk.
	st2, err := store.New(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if st2.PrimaryConversationID() != conv.ID {
		t.Errorf("primary conv id mismatch after reload")
	}
	msgs, _ := st2.ConversationHistory(conv.ID)
	if len(msgs) != 1 || msgs[0].Content != "hello" {
		t.Errorf("messages not persisted: %+v", msgs)
	}
	boundary, _ := st2.SummarizedUpTo(conv.ID)
	if boundary != 1 {
		t.Errorf("boundary not persisted: %d", boundary)
	}
	comps := st2.Compartments(conv.ID)
	if len(comps) != 1 || comps[0].ID == "" || comps[0].Tiers.P1 != "summary" {
		t.Errorf("compartments not persisted: %+v", comps)
	}
	mems := st2.Memories()
	if len(mems) != 1 || mems[0].Content != "durable fact" {
		t.Errorf("memories not persisted: %+v", mems)
	}
}
