// Package namepeer keeps one event-name list with the peer instance.
// simple-host.app's /v1/events hands out <name>.simple-hack.app records for
// self-hosted events; simple-hack.app hands out <event>.simple-hack.app for
// hosted ones. Each asks the other (EVENT_NAME_PEER, loopback) before taking a
// name: GET /internal/names/taken?zone=<zone>&name=<name> → {"taken": bool}.
// RegisterNamePeer answers that route; NamePeerClient asks it.
package namepeer
