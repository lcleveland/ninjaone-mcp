package tools

// Device writes: each acts on exactly one device (id), never a list.
var deviceWriteTools = []Tool{
	{Name: "ninjaone_device_action", Group: "inventory", Title: "Device actions",
		Description: "Act on one live device (id): maintenance mode, patch scans and installs, reboots, Windows services, policy overrides, backup. " +
			"Each call targets exactly one device. Confirm with the user before anything disruptive.",
		Views: []View{
			// device-maintenance
			{Action: "maintenance_set", Help: "put device id in maintenance; body {disabledFeatures: [ALERTS|PATCHING|AVSCANS|TASKS], start, end (epoch seconds)}. Reason is sent as the maintenance message.",
				Method: "PUT", Path: "/v2/device/{id}/maintenance", Capability: "device-maintenance", Body: true, ReasonField: "reasonMessage", Require: []string{"disabledFeatures", "end"}},
			{Action: "maintenance_cancel", Help: "end maintenance on device id.", Method: "DELETE", Path: "/v2/device/{id}/maintenance", Capability: "device-maintenance"},
			{Action: "patch_scan_os", Help: "scan device id for OS patches.", Method: "POST", Path: "/v2/device/{id}/patch/os/scan", Capability: "device-maintenance", Dispatch: true},
			{Action: "patch_scan_software", Help: "scan device id for third-party software patches.", Method: "POST", Path: "/v2/device/{id}/patch/software/scan", Capability: "device-maintenance", Dispatch: true},

			// device-actions
			{Action: "reboot", Help: "restart device id normally. Reason is sent to NinjaOne.", Method: "POST", Path: "/v2/device/{id}/reboot/NORMAL",
				Capability: "device-actions", ReasonField: "reason", Dispatch: true, Destructive: true},
			{Action: "reboot_forced", Help: "force-restart device id, losing unsaved user work. confirm must be the device's display name.", Method: "POST", Path: "/v2/device/{id}/reboot/FORCED",
				Capability: "device-actions", ReasonField: "reason", Dispatch: true, Confirm: true, Destructive: true},
			{Action: "service_control", Help: "start/stop/restart Windows service query.serviceId on device id; body {action: START|PAUSE|STOP|RESTART}.", Method: "POST",
				Path: "/v2/device/{id}/windows-service/{serviceId}/control", Capability: "device-actions", Body: true, Require: []string{"action"}, Dispatch: true, Destructive: true},
			{Action: "service_configure", Help: "change startup type (and account) of Windows service query.serviceId on device id; body {startType: AUTO_START|AUTO_START_DELAYED|DEMAND_START|DISABLED, userName}. Persists across reboots.", Method: "POST",
				Path: "/v2/device/{id}/windows-service/{serviceId}/configure", Capability: "device-actions", Body: true, Require: []string{"startType"}, Dispatch: true, Destructive: true},
			{Action: "patch_apply_os", Help: "install the OS patches policy has approved on device id; may reboot it depending on policy.", Method: "POST",
				Path: "/v2/device/{id}/patch/os/apply", Capability: "device-actions", Dispatch: true, Destructive: true},
			{Action: "patch_apply_software", Help: "install the software patches policy has approved on device id.", Method: "POST",
				Path: "/v2/device/{id}/patch/software/apply", Capability: "device-actions", Dispatch: true, Destructive: true},
			{Action: "reset_policy_overrides", Help: "remove every policy override on device id; the overrides cannot be recovered.", Method: "DELETE",
				Path: "/v2/device/{id}/policy/overrides", Capability: "device-actions", Dispatch: true, Destructive: true},
			{Action: "backup_integrity_check", Help: "start a backup integrity check; body {deviceId, planUid}. Returns its jobUid; follow it with ninjaone_backup integrity_jobs.", Method: "POST",
				Path: "/v2/backup/integrity-check-jobs", Capability: "device-actions", Body: true, Require: []string{"deviceId", "planUid"}},
			{Action: "backup_throttle", Help: "set backup bandwidth throttle on a device; body {deviceId, bandwidthThrottle}.", Method: "POST",
				Path: "/v2/backup/bandwidth-throttle", Capability: "device-actions", Body: true, Require: []string{"deviceId"}},
		}},
	{Name: "ninjaone_script", Group: "inventory", Title: "Scripts",
		Description: "Run a NinjaOne library script or built-in action on one device. This executes code on the machine, often as SYSTEM: confirm with the user first.",
		Views: []View{
			{Action: "options", Help: "scripts, built-in actions and credentials available to run on device id.", Path: "/v2/device/{id}/scripting/options", Single: true},
			{Action: "run", Help: "run on device id; body {type: SCRIPT|ACTION, id (script) or uid (action), parameters, runAs}. confirm must be the device's display name.", Method: "POST",
				Path: "/v2/device/{id}/script/run", Capability: "scripts", Body: true, Require: []string{"type"}, Dispatch: true, Confirm: true, Destructive: true},
		}},
}

// Device-admin actions live on the inventory tools they change.
var deviceAdminViews = []View{
	{Action: "update", Help: "change device id; body {displayName, userData, nodeRoleId, policyId, organizationId, locationId, warranty}. Moving organization or policy changes how it is managed.", Method: "PATCH",
		Path: "/v2/device/{id}", Capability: "device-admin", Body: true},
	{Action: "set_owner", Help: "assign query.ownerUid (an end user uid) as owner of device id.", Method: "POST",
		Path: "/v2/device/{id}/owner/{ownerUid}", Capability: "device-admin"},
	{Action: "remove_owner", Help: "clear the owner of device id.", Method: "DELETE", Path: "/v2/device/{id}/owner", Capability: "device-admin"},
	{Action: "approve", Help: "approve pending devices; body {devices: [id, ...]}.", Method: "POST",
		Path: "/v2/devices/approval/APPROVE", Capability: "device-admin", Body: true, Require: []string{"devices"}},
	{Action: "reject", Help: "reject pending devices; body {devices: [id, ...]}.", Method: "POST",
		Path: "/v2/devices/approval/REJECT", Capability: "device-admin", Body: true, Require: []string{"devices"}, Destructive: true},
	{Action: "decommission", Help: "stop managing device id; undoing it means reinstalling the agent. confirm must be the device's display name.", Method: "POST",
		Path: "/v2/device/{id}/decommission", Capability: "device-admin", Confirm: true, Destructive: true},
}

var orgAdminViews = []View{
	{Action: "create", Help: "create an organization; body {name, description, nodeApprovalMode, locations, policies}; query templateOrganizationId to copy one.", Method: "POST",
		Path: "/v2/organizations", Capability: "device-admin", Body: true, Require: []string{"name"}},
	{Action: "update", Help: "change organization id; body {name, description, nodeApprovalMode (AUTOMATIC lets any installer enroll devices)}.", Method: "PATCH",
		Path: "/v2/organization/{id}", Capability: "device-admin", Body: true},
	{Action: "create_location", Help: "add a location to organization id; body {name, address, description}.", Method: "POST",
		Path: "/v2/organization/{id}/locations", Capability: "device-admin", Body: true, Require: []string{"name"}},
	{Action: "update_location", Help: "change location query.locationId of organization id; body {name, address, description}.", Method: "PATCH",
		Path: "/v2/organization/{id}/locations/{locationId}", Capability: "device-admin", Body: true},
}

var alertWriteViews = []View{
	{Action: "reset", Help: "reset alert id (a uid), clearing the triggered condition.", Method: "POST", Path: "/v2/alert/{id}/reset", Capability: "device-maintenance"},
	{Action: "delete", Help: "delete alert id (a uid).", Method: "DELETE", Path: "/v2/alert/{id}", Capability: "device-maintenance"},
}
