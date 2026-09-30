// Package device provides the low-level, DP-centric bulb API — the
// equivalent of tinytuya's BulbDevice — on top of a protocol.Session,
// independent of which protocol version backs that session. Device exposes
// generic DP access (SetValue/SetDPs) alongside named operations (TurnOn,
// TurnOff, SetColour, SetBrightness, SetColourTemp, SetWhiteBrightness,
// SetColourTempPercent, SetMusicColour, Status) and knows what each DP means.
//
// Input validation follows one rule: the convenience helpers that take a
// bounded unit (the percent setters' 0-100) reject out-of-range input, since
// the bound is part of their contract. The raw helpers that take a DP's
// native int scale (SetColourTemp, SetBrightness) do not — they trust the caller with the DP's range, as
// tinytuya does, and let the device reject anything it dislikes.
//
// The high-level business surface (single-shot commands with no wait flag,
// plus the music-mode streaming loop) lives in the sibling package bulb,
// built on top of Device.
package device
