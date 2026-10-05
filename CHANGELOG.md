# Changelog

## [2.0.0](https://github.com/alex-bluetrain/notuya-go/compare/v1.0.0...v2.0.0) (2026-10-05)


### ⚠ BREAKING CHANGES

* **dp:** drop the legacy DP 1–8 schema
* pkg/device, pkg/protocol and pkg/protocol35 are removed. Use session/v35.Open, bulb.New and the pkg/dp types instead.

### Code Refactoring

* **dp:** drop the legacy DP 1–8 schema ([80f2e14](https://github.com/alex-bluetrain/notuya-go/commit/80f2e1484c7172398a69e2af379774b0c3b12156))
* split the library into transport, session, dp and bulb layers ([b04588e](https://github.com/alex-bluetrain/notuya-go/commit/b04588e98ea5fa6aaca59db4f45e5d564498c18e))

## [1.0.0](https://github.com/alex-bluetrain/notuya-go/compare/v0.1.0...v1.0.0) (2026-09-30)


### ⚠ BREAKING CHANGES

* cmd/notuya, cmd/notuyad and pkg/control are gone; StreamOptions/StreamColour.Transition is now ChangeMode *device.ChangeMode; SetMusicColour takes a device.ChangeMode; the removed helpers above are no longer exported.

### Code Refactoring

* reduce the module to the protocol library ([a561dc5](https://github.com/alex-bluetrain/notuya-go/commit/a561dc554fc0b8319771d9faf7c5037451e14114))
