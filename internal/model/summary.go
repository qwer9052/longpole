package model

import "sort"

// KindStat is the per-category rollup shown in the "time went to" block.
type KindStat struct {
	WorkNs int64
	WallNs int64
	Count  int
}

// Summary is everything the single-run report needs.
type Summary struct {
	Actions int
	Ran     int
	Cached  int

	// WorkNs sums subprocess time across all actions, so it exceeds WallNs on
	// a parallel build. WallNs is the observed span of the build.
	WorkNs  int64
	WallNs  int64
	QueueNs int64

	ByKind map[Kind]KindStat

	// Parallelism is WorkNs/WallNs: the average number of actions in flight.
	// Zero when wall time is unknown.
	Parallelism float64

	// FullyCached means real work existed and none of it ran. A build with no
	// work at all is not "fully cached", it is empty.
	FullyCached bool

	// QueueHeavy counts actions that waited more than 50ms to be scheduled.
	QueueHeavy int
}

// queueHeavyThresholdNs is the point past which queue wait is worth mentioning.
// Below this, scheduling jitter dominates and the number is noise.
const queueHeavyThresholdNs = 50_000_000

// Summarize rolls up a set of actions. wallNs is the observed build span,
// which the caller measures; pass 0 if it is unknown.
func Summarize(acts []Action, wallNs int64) Summary {
	s := Summary{
		Actions: len(acts),
		WallNs:  wallNs,
		ByKind:  make(map[Kind]KindStat),
	}
	workActions := 0
	for _, a := range acts {
		if a.Kind == KindCompile || a.Kind == KindLink {
			workActions++
		}
		if a.Ran {
			s.Ran++
		}
		if a.Cached {
			s.Cached++
		}
		s.WorkNs += a.WorkNs
		s.QueueNs += a.QueueNs
		if a.QueueNs > queueHeavyThresholdNs {
			s.QueueHeavy++
		}

		st := s.ByKind[a.Kind]
		st.WorkNs += a.WorkNs
		st.WallNs += a.WallNs
		st.Count++
		s.ByKind[a.Kind] = st
	}

	s.FullyCached = workActions > 0 && s.Ran == 0
	if wallNs > 0 {
		s.Parallelism = float64(s.WorkNs) / float64(wallNs)
	}
	return s
}

// TopByWork returns the n actions that consumed the most subprocess time,
// heaviest first. Cached actions are excluded: they did no work, and ranking
// them is exactly the mistake that makes existing tools misleading.
func TopByWork(acts []Action, n int) []Action {
	if n <= 0 {
		return nil
	}

	ran := make([]Action, 0, len(acts))
	for _, a := range acts {
		if a.Ran && a.WorkNs > 0 {
			ran = append(ran, a)
		}
	}
	sort.SliceStable(ran, func(i, j int) bool {
		return ran[i].WorkNs > ran[j].WorkNs
	})
	if len(ran) > n {
		ran = ran[:n]
	}
	return ran
}
