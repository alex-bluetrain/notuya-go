// Package bulb is the high-level business surface for one Tuya bulb —
// TurnOn, TurnOff, SetColour, SetWhiteBrightness,
// Status, and the
// music-mode colour streaming loop. It is built on top of device.Device
// (the low-level, DP-centric layer), the way the sibling Python project's
// ctl.py / picker.py drive tinytuya's BulbDevice. Callers that need a DP
// not covered here can reach the underlying Device via Raw().
package bulb
