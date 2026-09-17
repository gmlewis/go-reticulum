// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// newZeroHopEngine builds the exported zero-hop engine over the reference fix,
// with no hub, no socket, and no goroutine of its own.
func newZeroHopEngine(t *testing.T) *Engine {
	t.Helper()

	gps := NewGPSReader(nil)
	gps.SetFix(sfFix())
	t.Cleanup(func() {
		if err := gps.Close(); err != nil {
			t.Errorf("closing the GNSS source: %v", err)
		}
	})
	compass := NewCompassReader(nil)
	compass.SetHeading(portalReferenceHeading())
	t.Cleanup(func() {
		if err := compass.Close(); err != nil {
			t.Errorf("closing the compass source: %v", err)
		}
	})
	compass.SetLocationSource(func() (GPSFix, bool) { return gps.LastFix(), gps.LastFix().Valid })
	return NewEngine(defaultTestConfig(), BotPaths{Home: tempDir(t)}, gps, compass)
}

// TestEngineEvalAnswersAWhereAmIWithNoRadioLink asserts the zero-hop promise:
// the engine computes the full operational card in-process, so the same
// survival intelligence the radio would carry is available when no hub is
// reachable at all.
func TestEngineEvalAnswersAWhereAmIWithNoRadioLink(t *testing.T) {
	t.Parallel()

	engine := newZeroHopEngine(t)
	got, err := engine.Eval(context.Background(), "whereami")
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	for _, want := range []string{
		"/whereami",
		refPlus10,
		refGrid,
		"37.75532° N",
		"122.45272° W",
		"142 m (467 ft) MSL",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the card does not contain %q:\n%v", want, got)
		}
	}
}

// TestEngineEvalJoinsArguments asserts a command with arguments is evaluated
// exactly as a chat client would have sent it: the words after the command
// word, joined by single spaces.
func TestEngineEvalJoinsArguments(t *testing.T) {
	t.Parallel()

	engine := newZeroHopEngine(t)
	explicit, err := engine.Eval(context.Background(), "whereami", "37.7553,-122.4527")
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if !strings.Contains(explicit, refPlus10) {
		t.Errorf("the explicit-position card does not contain %q:\n%v", refPlus10, explicit)
	}
	if explicit != strings.Join(engine.RunLocal("whereami 37.7553,-122.4527"), "\n") {
		t.Error("Eval and RunLocal disagree about the same command line")
	}
}

// TestEngineEvalWithoutSensorsSaysSo asserts the engine is honest with no
// hardware behind it: it reports that no fix has arrived rather than inventing
// a position.
func TestEngineEvalWithoutSensorsSaysSo(t *testing.T) {
	t.Parallel()

	engine := NewEngine(defaultTestConfig(), BotPaths{}, nil, nil)
	got, err := engine.Eval(context.Background(), "whereami")
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if !strings.Contains(got, whereamiNoFixLine) {
		t.Errorf("Eval(whereami) = %q, want the no-fix line %q", got, whereamiNoFixLine)
	}
}

// TestEngineEvalReportsAnUnknownCommand asserts a typo is answered the way the
// radio answers it, so an operator debugging offline sees the same wording.
func TestEngineEvalReportsAnUnknownCommand(t *testing.T) {
	t.Parallel()

	engine := newZeroHopEngine(t)
	got, err := engine.Eval(context.Background(), "bogus")
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if !strings.Contains(got, "unknown command") {
		t.Errorf("Eval(bogus) = %q, want it to report an unknown command", got)
	}
}

// TestEngineEvalRefusesALinkOnlyCommand asserts the engine never pretends a
// command that needs a live hub link worked: it names the limitation instead of
// failing obscurely or answering from nothing.
func TestEngineEvalRefusesALinkOnlyCommand(t *testing.T) {
	t.Parallel()

	engine := newZeroHopEngine(t)
	got, err := engine.Eval(context.Background(), "path")
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if !strings.Contains(got, "needs a live hub link") {
		t.Errorf("Eval(path) = %q, want it to say the command needs a live link", got)
	}
}

// TestEngineEvalHonoursACancelledContext asserts a caller that has already
// given up is not made to wait for an answer.
func TestEngineEvalHonoursACancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	engine := newZeroHopEngine(t)
	if _, err := engine.Eval(ctx, "whereami"); err == nil {
		t.Fatal("Eval answered with a cancelled context, want an error")
	}
}

// TestEngineCommandLists asserts the engine exposes both the whole command set
// and the offline subset, sorted, with the subset strictly inside the whole.
func TestEngineCommandLists(t *testing.T) {
	t.Parallel()

	engine := newZeroHopEngine(t)
	all := engine.Commands()
	if len(all) == 0 {
		t.Fatal("Commands() is empty")
	}
	if !slices.IsSorted(all) {
		t.Errorf("Commands() = %v, want it sorted", all)
	}
	offline := engine.OfflineCommands()
	if len(offline) == 0 {
		t.Fatal("OfflineCommands() is empty")
	}
	if !slices.IsSorted(offline) {
		t.Errorf("OfflineCommands() = %v, want it sorted", offline)
	}
	for _, name := range offline {
		if !slices.Contains(all, name) {
			t.Errorf("the offline command %q is not in the full command set", name)
		}
	}
	if slices.Contains(offline, "path") {
		t.Error("path is offered offline, but it needs a live hub link")
	}
}

// TestEngineWithoutAConfigStillEvaluates asserts the engine tolerates being
// built without a configuration, which is what a bare portal test does, and
// that a pure field tool still answers from nothing but its arguments.
func TestEngineWithoutAConfigStillEvaluates(t *testing.T) {
	t.Parallel()

	engine := NewEngine(nil, BotPaths{}, nil, nil)
	if engine.Config() != nil {
		t.Error("Config() is not nil for an engine built without one")
	}
	got, err := engine.Eval(context.Background(), "morse", "SOS")
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if !strings.Contains(got, "... --- ...") {
		t.Errorf("Eval(morse SOS) = %q, want the Morse for SOS", got)
	}
}

// TestEngineBacksThePortalDashboard asserts the portal reads its answers from
// the same engine the radio would, so the dashboard and the radio can never
// disagree, and that a portal built from an engine needs no hub link.
func TestEngineBacksThePortalDashboard(t *testing.T) {
	t.Parallel()

	engine := newZeroHopEngine(t)
	gps := NewGPSReader(nil)
	gps.SetFix(sfFix())
	t.Cleanup(func() {
		if err := gps.Close(); err != nil {
			t.Errorf("closing the GNSS source: %v", err)
		}
	})
	portal := NewPortalServer("127.0.0.1:0", gps, engine)
	portal.now = func() time.Time { return whereamiTestNow }
	t.Cleanup(func() {
		if err := portal.Close(); err != nil {
			t.Errorf("closing the portal: %v", err)
		}
	})

	rec := httptest.NewRecorder()
	portal.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/whereami", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/whereami = %v, want %v", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), refPlus10) {
		t.Errorf("the dashboard position does not carry %q:\n%v", refPlus10, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	portal.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/compass", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/compass = %v, want %v", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "42") {
		t.Errorf("the dashboard compass does not carry the reference true heading:\n%v", rec.Body.String())
	}
}
