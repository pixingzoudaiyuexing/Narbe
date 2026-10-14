package app

import "time"

const loginSourceCapacity = 4096

// Source capacity bounds memory, not admission. Prefer keeping already limited
// sources when evicting so a stream of first attempts cannot reset their limit.
func (a *App) allowLogin(host string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	recent := a.attempts[host][:0]
	for _, t := range a.attempts[host] {
		if now.Sub(t) < 10*time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= 8 {
		a.attempts[host] = recent
		return false
	}
	if len(recent) == 0 && len(a.attempts) >= loginSourceCapacity {
		victim := ""
		var oldest time.Time
		victimLimited := true
		for source, items := range a.attempts {
			active := items[:0]
			for _, t := range items {
				if now.Sub(t) < 10*time.Minute {
					active = append(active, t)
				}
			}
			if len(active) == 0 {
				delete(a.attempts, source)
				continue
			}
			a.attempts[source] = active
			limited := len(active) >= 8
			last := active[len(active)-1]
			if victim == "" || (victimLimited && !limited) || (victimLimited == limited && last.Before(oldest)) {
				victim = source
				oldest = last
				victimLimited = limited
			}
		}
		if len(a.attempts) >= loginSourceCapacity {
			delete(a.attempts, victim)
		}
	}
	a.attempts[host] = append(recent, now)
	return true
}
