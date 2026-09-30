package scan

import (
	"github.com/rutvikchandla3/paircli/internal/engine"
	"github.com/rutvikchandla3/paircli/internal/enrich"
	"github.com/rutvikchandla3/paircli/internal/judge"
	"github.com/rutvikchandla3/paircli/internal/model"
)

// inferredIDs are the three signals only the LLM pass produces (T26).
var inferredIDs = []string{"DEC-3", "CON-1", "CON-3"}

// Merge folds the outcome of a grounded LLM pass into the deterministic
// signals and returns the set the report carries. ec.Judgments is set to j as
// a side effect: the inferred detectors and enrich both read it from there.
//
// It is the one merge rule, shared by the live pipeline (Run) and the replay
// path (Replay), so the two cannot drift apart. The order is load-bearing:
//
//   - a failure is recorded on AUTH-2 first, so the report footer can say the
//     pass was incomplete whether it failed as a whole (passErr, with no
//     judgments at all) or only in part (j.Errors, with the other jobs'
//     results kept);
//   - the inferred signals are recomputed from the judgments, replacing the
//     "no LLM pass" versions engine.Run produced;
//   - an inferred signal whose input was unusable becomes unknown rather than
//     clear — "we could not check" is not "we checked and found nothing";
//   - enrich folds the judgments into the deterministic signals last, because
//     it appends to summaries markUnresolved would otherwise overwrite.
//
// PassErrors reports the same failures for a caller that wants to surface them
// beyond the report.
func Merge(ec *engine.Context, sigs []model.Signal, j *model.Judgments, stats *judge.Stats, passErr error) []model.Signal {
	if msgs := PassErrors(j, passErr); len(msgs) > 0 {
		sigs = noteLLMError(sigs, msgs)
	}
	if passErr != nil {
		// The whole pass failed: keep the deterministic output.
		j, stats = nil, nil
	}
	ec.Judgments = j
	if j == nil {
		return sigs
	}
	sigs = replaceByID(sigs, engine.RunIDs(ec, inferredIDs...))
	sigs = markUnresolved(sigs, stats)
	return enrich.Apply(ec, sigs)
}

// PassErrors lists what an LLM pass could not do: the error that stopped it
// entirely, or one entry per job that produced nothing usable while the others
// answered. It is empty when the pass was clean, and when it did not run.
func PassErrors(j *model.Judgments, passErr error) []string {
	switch {
	case passErr != nil:
		return []string{passErr.Error()}
	case j != nil && len(j.Errors) > 0:
		return append([]string(nil), j.Errors...)
	}
	return nil
}

// replaceByID returns sigs with every signal in repl swapped in for the signal
// of the same id, in place; a signal sigs does not carry is appended.
func replaceByID(sigs []model.Signal, repl []model.Signal) []model.Signal {
	for _, r := range repl {
		done := false
		for i := range sigs {
			if sigs[i].ID == r.ID {
				sigs[i] = r
				done = true
				break
			}
		}
		if !done {
			sigs = append(sigs, r)
		}
	}
	return sigs
}

// noteLLMError records a failed LLM pass on AUTH-2's Data, the signal the
// report footer is built from, and returns sigs unchanged otherwise.
func noteLLMError(sigs []model.Signal, msgs []string) []model.Signal {
	if len(msgs) == 0 {
		return sigs
	}
	for i := range sigs {
		if sigs[i].ID != "AUTH-2" {
			continue
		}
		data := make(map[string]any, len(sigs[i].Data)+1)
		for k, v := range sigs[i].Data {
			data[k] = v
		}
		data["llm_errors"] = msgs
		sigs[i].Data = data
		break
	}
	return sigs
}

// markUnresolved replaces each inferred signal whose judgment input was
// unusable with an unknown signal. Without it, a job that failed or whose items
// were all dropped by validation leaves the signal reporting clear — which
// reads as "checked, nothing found" when the truth is "nothing was checked".
//
// Only the inferred signals are touched. Every other signal carries a
// deterministic measurement that an unusable judgment result must not
// overwrite, and marking those unknown would throw away real evidence.
func markUnresolved(sigs []model.Signal, stats *judge.Stats) []model.Signal {
	for _, u := range stats.Unresolved() {
		for i := range sigs {
			if sigs[i].ID != u.Signal {
				continue
			}
			sigs[i] = engine.Unknown(u.Signal, u.Reason)
			if u.Attempted > 0 {
				sigs[i].Data = map[string]any{"llm_dropped": u.Attempted}
			}
			break
		}
	}
	return sigs
}
