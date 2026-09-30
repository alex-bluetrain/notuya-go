# Changelog

## [1.0.0](https://github.com/alex-bluetrain/notuya-go/compare/v0.1.0...v1.0.0) (2026-09-30)


### ⚠ BREAKING CHANGES

* cmd/notuya, cmd/notuyad and pkg/control are gone; StreamOptions/StreamColour.Transition is now ChangeMode *device.ChangeMode; SetMusicColour takes a device.ChangeMode; the removed helpers above are no longer exported.

### Code Refactoring

* reduce the module to the protocol library ([a561dc5](https://github.com/alex-bluetrain/notuya-go/commit/a561dc554fc0b8319771d9faf7c5037451e14114))
