package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/averstraeten/notuya-go/pkg/discovery"
)

// updateConfigIPs rewrites the ip_address of every device in the config file
// whose device_id appears in a scan result, matching tinytuya's wizard: the
// scan never learns a local_key, so devices are matched by ID and only their
// IP (the thing DHCP changes) is touched.
//
// The file is edited as a generic JSON tree rather than reserialised from the
// typed Config, so top-level keys this project does not model (theme/wallpaper
// keys from the sibling Python project) and any extra per-device fields survive
// the rewrite untouched.
//
// It returns whether the write succeeded; a scan that finds nothing is not a
// failure (it prints that and leaves the file alone), but an unreadable or
// unwritable config is.
func updateConfigIPs(path string, found []discovery.Device) bool {
	byID := make(map[string]string, len(found))
	for _, d := range found {
		if d.ID != "" && d.IP != "" {
			byID[d.ID] = d.IP
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scan --update: reading %s: %v\n", path, err)
		return false
	}

	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		fmt.Fprintf(os.Stderr, "scan --update: parsing %s: %v\n", path, err)
		return false
	}

	rawDevices, ok := root["devices"]
	if !ok {
		fmt.Fprintf(os.Stderr, "scan --update: no devices array in %s\n", path)
		return false
	}
	var devices []map[string]any
	if err := json.Unmarshal(rawDevices, &devices); err != nil {
		fmt.Fprintf(os.Stderr, "scan --update: parsing devices in %s: %v\n", path, err)
		return false
	}

	changed := 0
	for _, dev := range devices {
		id, _ := dev["device_id"].(string)
		newIP, seen := byID[id]
		if !seen {
			continue
		}
		oldIP, _ := dev["ip_address"].(string)
		if oldIP == newIP {
			continue
		}
		name, _ := dev["name"].(string)
		if name == "" {
			name = id
		}
		fmt.Printf("%s: %s -> %s\n", name, oldIP, newIP)
		dev["ip_address"] = newIP
		changed++
	}

	if changed == 0 {
		fmt.Println("scan --update: no IP changes")
		return true
	}

	updated, err := json.Marshal(devices)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scan --update: encoding devices: %v\n", err)
		return false
	}
	root["devices"] = updated

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "scan --update: encoding %s: %v\n", path, err)
		return false
	}
	out = append(out, '\n')
	if err := os.WriteFile(path, out, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "scan --update: writing %s: %v\n", path, err)
		return false
	}

	fmt.Printf("scan --update: updated %d device(s) in %s\n", changed, path)
	return true
}
