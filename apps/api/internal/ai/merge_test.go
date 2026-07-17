package ai

import "testing"

func TestMergePlacesWithSource(t *testing.T) {
	caption := []Place{
		{Name: "Fake Cafe", Hint: "Faketown", Confidence: 0.6},
		{Name: "Fake Museum", Hint: "", Confidence: 0.7},
	}
	vision := []Place{
		{Name: "Fake Cafe", Hint: "Faketown", Confidence: 0.95},
		{Name: "Vision Landmark", Hint: "Faketown", Confidence: 0.8},
	}

	got := MergePlacesWithSource(caption, vision)
	if len(got) != 3 {
		t.Fatalf("got %d places, want 3", len(got))
	}

	byName := map[string]Placed{}
	for _, p := range got {
		byName[p.Name] = p
	}

	cafe := byName["Fake Cafe"]
	if cafe.Source != "vision" || cafe.Confidence != 0.95 {
		t.Errorf("Fake Cafe = %+v, want vision @ 0.95", cafe)
	}
	museum := byName["Fake Museum"]
	if museum.Source != "caption" {
		t.Errorf("Fake Museum source = %q, want caption", museum.Source)
	}
	landmark := byName["Vision Landmark"]
	if landmark.Source != "vision" {
		t.Errorf("Vision Landmark source = %q, want vision", landmark.Source)
	}
}

func TestMergePlacesWithSource_TieKeepsCaption(t *testing.T) {
	caption := []Place{{Name: "Same Spot", Hint: "A", Confidence: 0.8}}
	vision := []Place{{Name: "same spot", Hint: "B", Confidence: 0.8}}
	got := MergePlacesWithSource(caption, vision)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0].Source != "caption" || got[0].Hint != "A" {
		t.Errorf("tie should keep caption: %+v", got[0])
	}
}

func TestMergePlacesWithSource_DefaultConfidence(t *testing.T) {
	// Missing confidence (0) gets source defaults; vision default 0.7 < caption 0.85.
	caption := []Place{{Name: "Spot", Hint: ""}}
	vision := []Place{{Name: "Spot", Hint: "elsewhere"}}
	got := MergePlacesWithSource(caption, vision)
	if len(got) != 1 || got[0].Source != "caption" || got[0].Confidence != 0.85 {
		t.Errorf("default confidence merge = %+v, want caption @ 0.85", got)
	}
}

func TestSanitisePlaces_ClampsConfidence(t *testing.T) {
	got := sanitisePlaces([]Place{
		{Name: "A", Confidence: 1.5},
		{Name: "B", Confidence: -0.2},
		{Name: ""},
		{Name: "A"}, // duplicate dropped
	})
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	if got[0].Confidence != 1 || got[1].Confidence != 0 {
		t.Errorf("clamped = %+v", got)
	}
}
