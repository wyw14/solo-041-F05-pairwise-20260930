package settling

import (
	"fmt"
	"time"
)

// DecantDelays reports why a settling cycle may not advance into the decant
// phase yet, based on the current blanket measurement, the evidence gathered
// during the cycle and the line's alarm state. An empty result means the
// transition is allowed.
func DecantDelays(snapshot Snapshot, activeAlarms int, now time.Time) []string {
	var reasons []string
	if snapshot.High {
		reasons = append(reasons, fmt.Sprintf("sludge blanket %.2f m at/above high threshold %.2f m", snapshot.BlanketLevel, BlanketHighThreshold))
	}
	if snapshot.CycleSamples < MinCycleBlanketSamples {
		reasons = append(reasons, fmt.Sprintf("blanket samples this cycle %d/%d", snapshot.CycleSamples, MinCycleBlanketSamples))
	}
	if snapshot.StableSince != nil {
		if stable := now.UTC().Sub(*snapshot.StableSince); stable < MinBlanketStable {
			reasons = append(reasons, fmt.Sprintf("blanket stable for %s, requires %s", stable.Truncate(time.Second), MinBlanketStable))
		}
	}
	if activeAlarms > 0 {
		reasons = append(reasons, fmt.Sprintf("%d active alarm(s) on line %s", activeAlarms, snapshot.LineID))
	}
	return reasons
}
