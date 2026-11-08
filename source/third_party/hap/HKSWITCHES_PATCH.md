# Local patch

This is github.com/brutella/hap v0.0.35, vendored unchanged except for
characteristic/c.go (search for "hkswitches patch"):

- Added `func (c *C) SetUpdateOnSameValue(b bool)`.
- `setValue` reads `updateOnSameValue` while holding `c.m`.

hkswitches uses this so a HomeKit write of the value a switch already has
still reaches `OnValueRemoteUpdate` (used to restart auto-off timers).
