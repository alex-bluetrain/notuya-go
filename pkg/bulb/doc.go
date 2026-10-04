// Package bulb is the application layer for one Tuya bulb: domain verbs
// (TurnOn, SetColour, SetWhiteBrightness, SetScene, SetTimer, …), status
// and capabilities, pushed state changes (Watch), and the colour streaming
// loop. It is built on a session.Session for messaging and pkg/dp for DP
// encoding; it never names a message code or a DP number itself.
//
// Set writes arbitrary dp.Values for DPs the named methods do not cover.
package bulb
