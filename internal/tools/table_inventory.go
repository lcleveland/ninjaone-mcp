package tools

import "strings"

var deviceBrief = []string{"id", "systemName", "displayName", "dnsName", "nodeClass", "organizationId", "locationId", "offline", "lastContact", "approvalStatus"}

var inventoryTools = []Tool{
	{Name: "ninjaone_organization", Group: "inventory", Title: "Organizations",
		Description: "NinjaOne organizations (the customers or sites that own locations and devices).",
		Views: []View{
			{Action: "list", Help: "all organizations.", Path: "/v2/organizations", Paging: After,
				Brief: []string{"id", "name", "description", "nodeApprovalMode"}},
			{Action: "get", Help: "one organization (id) with its locations, policies and settings.", Path: "/v2/organization/{id}", Single: true},
			{Action: "locations", Help: "the locations of organization id.", Path: "/v2/organization/{id}/locations"},
			{Action: "devices", Help: "the devices of organization id.", Path: "/v2/organization/{id}/devices", Paging: After, Brief: deviceBrief},
			{Action: "end_users", Help: "the end users of organization id.", Path: "/v2/organization/{id}/end-users"},
		}},
	{Name: "ninjaone_location", Group: "inventory", Title: "Locations",
		Description: "NinjaOne locations across all organizations. For one organization's locations use ninjaone_organization action locations.",
		Views: []View{
			{Action: "list", Help: "all locations, with organizationId.", Path: "/v2/locations", Paging: After,
				Brief: []string{"id", "name", "organizationId", "address", "description"}},
		}},
	{Name: "ninjaone_device", Group: "inventory", Title: "Devices",
		Description: "Devices managed by NinjaOne. To find one by name, user or IP use search; to select by organization, location, role, class or status use list with df.",
		Views: []View{
			{Action: "list", Help: "devices matching df (or all). Brief items include lastContact and offline.", Path: "/v2/devices", Paging: After, DF: true, Brief: deviceBrief},
			{Action: "search", Help: "free-text search by name, logged-on user or IP: query {\"q\": \"...\"}.", Path: "/v2/devices/search",
				Items: "devices", LimitParam: "limit", Brief: deviceBrief},
			{Action: "get", Help: "one device (id) in full, plus _dashboard_url linking to it in the NinjaOne console.", Path: "/v2/device/{id}", Single: true, DeepLink: true},
		}},
	{Name: "ninjaone_device_detail", Group: "inventory", Title: "Device details",
		Description: "Hardware, software and state of one device (id). For the same data across many devices use ninjaone_report.",
		Views: []View{
			{Action: "disks", Help: "physical disks.", Path: "/v2/device/{id}/disks"},
			{Action: "volumes", Help: "volumes and free space.", Path: "/v2/device/{id}/volumes"},
			{Action: "processors", Help: "CPUs.", Path: "/v2/device/{id}/processors"},
			{Action: "network_interfaces", Help: "network adapters with IPs and MACs.", Path: "/v2/device/{id}/network-interfaces"},
			{Action: "software", Help: "installed software (name, version, publisher, installDate).", Path: "/v2/device/{id}/software",
				Brief: []string{"name", "version", "publisher", "installDate"}},
			{Action: "windows_services", Help: "Windows services and their state; query {\"name\": ..., \"state\": ...}.", Path: "/v2/device/{id}/windows-services"},
			{Action: "last_logged_on_user", Help: "the last user to log on.", Path: "/v2/device/{id}/last-logged-on-user", Single: true},
			{Action: "policy_overrides", Help: "policy settings overridden on this device.", Path: "/v2/device/{id}/policy/overrides", Single: true},
			{Action: "jobs", Help: "jobs currently running on the device.", Path: "/v2/device/{id}/jobs",
				Brief: []string{"uid", "jobType", "jobStatus", "jobResult", "subject", "message", "createTime", "updateTime"}},
		}},
	{Name: "ninjaone_report", Group: "inventory", Title: "Fleet reports",
		Description: "Fleet-wide reports: one row per device (or per item), filtered by df. Use these instead of looping over devices.",
		Views: reports(
			"computer_systems: make, model, serial number, memory",
			"operating_systems: OS name, build, architecture, last boot",
			"software: installed software; query installedBefore/installedAfter",
			"device_health: health summary; query {\"health\": \"UNHEALTHY\"}",
			"processors: CPUs",
			"disks: physical disks",
			"volumes: volumes and free space",
			"network_interfaces: adapters, IPs, MACs",
			"raid_controllers: RAID controllers",
			"raid_drives: RAID member drives",
			"logged_on_users: who is logged on where",
			"windows_services: Windows services; query name/state",
			"antivirus_status: AV product and state",
			"antivirus_threats: AV detections",
			"policy_overrides: devices with policy overrides",
		)},
	{Name: "ninjaone_lookup", Group: "inventory", Title: "Policies, roles and groups",
		Description: "Lookups that give meaning to ids elsewhere: policyId, nodeRoleId (df role = ...) and saved groups (df group ...).",
		Views: []View{
			{Action: "policies", Help: "policies.", Path: "/v2/policies"},
			{Action: "device_roles", Help: "device roles (nodeRoleId).", Path: "/v2/roles"},
			{Action: "groups", Help: "saved device groups (searches).", Path: "/v2/groups"},
			{Action: "group_device_ids", Help: "device ids in group id.", Path: "/v2/group/{id}/device-ids"},
		}},
	{Name: "ninjaone_user", Group: "inventory", Title: "Users and contacts",
		Description: "NinjaOne technicians, end users, their roles, and contacts.",
		Views: []View{
			{Action: "users", Help: "all users; query userType (TECHNICIAN|END_USER), includeRoles.", Path: "/v2/users",
				Brief: []string{"id", "uid", "firstName", "lastName", "email", "userType", "enabled", "administrator", "organizationId"}},
			{Action: "technicians", Help: "technicians.", Path: "/v2/user/technicians"},
			{Action: "end_users", Help: "end users.", Path: "/v2/user/end-users"},
			{Action: "roles", Help: "user roles and members; query roleType.", Path: "/v2/user/roles"},
			{Action: "contacts", Help: "contacts.", Path: "/v2/contacts"},
		}},
}

// reports builds one cursor-paged /v2/queries/* view per "name: help" line.
func reports(lines ...string) []View {
	vs := make([]View, len(lines))
	for i, l := range lines {
		name, h, _ := strings.Cut(l, ": ")
		vs[i] = View{Action: name, Help: h + ".", Path: "/v2/queries/" + strings.ReplaceAll(name, "_", "-"),
			Paging: Cursor, Items: "results", DF: true}
	}
	return vs
}
