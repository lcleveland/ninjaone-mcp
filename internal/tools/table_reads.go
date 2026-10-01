package tools

var (
	alertBrief    = []string{"uid", "deviceId", "severity", "priority", "sourceType", "sourceName", "conditionName", "subject", "message", "createTime", "updateTime"}
	activityBrief = []string{"id", "activityTime", "deviceId", "severity", "priority", "seriesUid", "activityType", "statusCode", "status", "activityResult", "sourceName", "subject", "message", "userId"}
	patchBrief    = []string{"id", "deviceId", "name", "kbNumber", "productIdentifier", "title", "severity", "impact", "status", "type", "installedAt", "timestamp"}
	jobBrief      = []string{"uid", "deviceId", "jobType", "jobStatus", "jobResult", "subject", "message", "createTime", "updateTime"}
)

var readTools = []Tool{
	// alerts
	{Name: "ninjaone_alert", Group: "alerts", Title: "Alerts",
		Description: "Active NinjaOne alerts: conditions currently triggered on devices. Not paged; narrow with df or sourceType.",
		Views: []View{
			{Action: "list", Help: "active alerts across the fleet; df, query sourceType.", Path: "/v2/alerts", DF: true, Brief: alertBrief},
			{Action: "device", Help: "active alerts on device id.", Path: "/v2/device/{id}/alerts", Brief: alertBrief},
		}},
	{Name: "ninjaone_activity", Group: "alerts", Title: "Activities and jobs",
		Description: "What happened: the NinjaOne activity log (newest first), and jobs running now. " +
			"After a device action, list that device's activities with query {\"newerThan\": <activity id>} to see its outcome.",
		Views: []View{
			{Action: "list", Help: "activities across the fleet; df, query class (SYSTEM|DEVICE|USER|ALL), type, status, seriesUid (ties activities to an alert), user, after/before (yyyyMMdd), newerThan (activity id).",
				Path: "/v2/activities", Paging: Activity, Items: "activities", DF: true, Brief: activityBrief},
			{Action: "device", Help: "activities on device id; query activityType, status, seriesUid, newerThan (activity id).",
				Path: "/v2/device/{id}/activities", Paging: Activity, Items: "activities", Brief: activityBrief},
			{Action: "active_jobs", Help: "jobs running now across the fleet; df, query jobType.", Path: "/v2/jobs", DF: true, Brief: jobBrief},
		}},

	// patching
	{Name: "ninjaone_patch", Group: "patching", Title: "Patches",
		Description: "OS and third-party software patches: pending, failed or rejected patches, and install history. device_* actions take a device id; fleet_* actions take df. " +
			"Query status, type, severity (OS) or impact/productIdentifier (software), installedBefore/installedAfter (installs).",
		Views: []View{
			{Action: "device_os", Help: "pending/failed/rejected OS patches on device id.", Path: "/v2/device/{id}/os-patches", Brief: patchBrief},
			{Action: "device_os_installs", Help: "OS patch install history on device id.", Path: "/v2/device/{id}/os-patch-installs", Brief: patchBrief},
			{Action: "device_software", Help: "pending/failed/rejected software patches on device id.", Path: "/v2/device/{id}/software-patches", Brief: patchBrief},
			{Action: "device_software_installs", Help: "software patch install history on device id.", Path: "/v2/device/{id}/software-patch-installs", Brief: patchBrief},
			{Action: "fleet_os", Help: "OS patches across devices matching df.", Path: "/v2/queries/os-patches", Paging: Cursor, Items: "results", DF: true},
			{Action: "fleet_os_installs", Help: "OS patch installs across devices matching df.", Path: "/v2/queries/os-patch-installs", Paging: Cursor, Items: "results", DF: true},
			{Action: "fleet_software", Help: "software patches across devices matching df.", Path: "/v2/queries/software-patches", Paging: Cursor, Items: "results", DF: true},
			{Action: "fleet_software_installs", Help: "software patch installs across devices matching df.", Path: "/v2/queries/software-patch-installs", Paging: Cursor, Items: "results", DF: true},
		}},

	// docs
	{Name: "ninjaone_custom_field", Group: "docs", Title: "Custom fields",
		Description: "Custom field values on devices, organizations, locations and end users, and their definitions. Secure (encrypted) values are never returned.",
		Views: []View{
			{Action: "device", Help: "values on device id, keyed by field name; query withInheritance.", Path: "/v2/device/{id}/custom-fields", Single: true},
			{Action: "organization", Help: "values on organization id.", Path: "/v2/organization/{id}/custom-fields", Single: true},
			{Action: "location", Help: "values on location query.locationId of organization id; query withInheritance.", Path: "/v2/organization/{id}/location/{locationId}/custom-fields", Single: true},
			{Action: "end_user", Help: "values on end user id.", Path: "/v2/user/end-user/{id}/custom-fields", Single: true},
			{Action: "global", Help: "global (system-wide) values.", Path: "/v2/system/custom-fields", Single: true},
			{Action: "fleet", Help: "values across devices matching df; query fields (comma list of field names), updatedAfter.", Path: "/v2/queries/custom-fields", Paging: Cursor, Items: "results", DF: true},
			{Action: "fleet_scoped", Help: "values across organizations/locations/devices; query scopes, fields, updatedAfter.", Path: "/v2/queries/scoped-custom-fields", Paging: Cursor, Items: "results", DF: true},
			{Action: "definitions", Help: "field definitions (name, label, type, scope, permissions); query scopes.", Path: "/v2/device-custom-fields",
				Brief: []string{"name", "label", "type", "definitionScope", "description", "apiPermission"}},
			{Action: "set_device", Help: "set values on device id; body {fieldName: value, ...}. Dates are epoch milliseconds; dropdowns take option ids.", Method: "PATCH",
				Path: "/v2/device/{id}/custom-fields", Capability: "custom-fields", Body: true},
			{Action: "set_organization", Help: "set values on organization id; body {fieldName: value}.", Method: "PATCH",
				Path: "/v2/organization/{id}/custom-fields", Capability: "custom-fields", Body: true},
			{Action: "set_location", Help: "set values on location query.locationId of organization id; body {fieldName: value}.", Method: "PATCH",
				Path: "/v2/organization/{id}/location/{locationId}/custom-fields", Capability: "custom-fields", Body: true},
			{Action: "set_end_user", Help: "set values on end user id; body {fieldName: value}.", Method: "PATCH",
				Path: "/v2/user/end-user/{id}/custom-fields", Capability: "custom-fields", Body: true},
		}},
	{Name: "ninjaone_document", Group: "docs", Title: "Documentation",
		Description: "NinjaOne Documentation: structured documents per organization and the templates that define them.",
		Views: []View{
			{Action: "list", Help: "documents; query organizationIds, templateIds, templateName, documentName (comma lists).", Path: "/v2/organization/documents",
				Brief: []string{"documentId", "documentName", "documentDescription", "documentTemplateName", "organizationId", "documentUpdateTime"}},
			{Action: "organization", Help: "documents of organization id, with their fields.", Path: "/v2/organization/{id}/documents"},
			{Action: "templates", Help: "document templates; query templateName.", Path: "/v2/document-templates",
				Brief: []string{"id", "name", "description", "archived", "allowMultiple", "mandatory"}},
			{Action: "template", Help: "one template (id) with its field definitions.", Path: "/v2/document-templates/{id}", Single: true},
			{Action: "create", Help: "create documents; body [{organizationId, documentTemplateId, documentName, documentDescription, fields}].", Method: "POST",
				Path: "/v2/organization/documents", Capability: "documentation", Body: true, Bulk: true},
			{Action: "update", Help: "update documents; body [{documentId, documentName, documentDescription, fields}]. Fields you send replace their values.", Method: "PATCH",
				Path: "/v2/organization/documents", Capability: "documentation", Body: true, Bulk: true},
			{Action: "archive", Help: "archive documents; body [documentId, ...]. Reversible with restore.", Method: "POST",
				Path: "/v2/organization/documents/archive", Capability: "documentation", Body: true, Bulk: true},
			{Action: "restore", Help: "restore archived documents; body [documentId, ...].", Method: "POST",
				Path: "/v2/organization/documents/restore", Capability: "documentation", Body: true, Bulk: true},
			{Action: "delete", Help: "permanently delete archived document id.", Method: "DELETE",
				Path: "/v2/organization/document/{id}", Capability: "documentation", Destructive: true},
			{Action: "create_template", Help: "create a template; body {name, description, fields, allowMultiple, mandatory, ...}.", Method: "POST",
				Path: "/v2/document-templates", Capability: "documentation", Body: true},
			{Action: "update_template", Help: "update template id; body {name, description, fields, ...}.", Method: "PUT",
				Path: "/v2/document-templates/{id}", Capability: "documentation", Body: true},
			{Action: "archive_template", Help: "archive template id.", Method: "POST",
				Path: "/v2/document-templates/{id}/archive", Capability: "documentation"},
			{Action: "restore_template", Help: "restore archived template id.", Method: "POST",
				Path: "/v2/document-templates/{id}/restore", Capability: "documentation"},
		}},
	{Name: "ninjaone_kb_article", Group: "docs", Title: "Knowledge base",
		Description: "NinjaOne knowledge base articles and folders, global or per organization. Attachment links are signed URLs that expire.",
		Views: []View{
			{Action: "organization", Help: "organization articles; query organizationIds, articleName, includeArchived.", Path: "/v2/knowledgebase/organization/articles",
				Brief: []string{"id", "name", "path", "organizationId", "parentFolderId", "isArchived", "updateTime"}},
			{Action: "global", Help: "global articles; query articleName, includeArchived.", Path: "/v2/knowledgebase/global/articles",
				Brief: []string{"id", "name", "path", "parentFolderId", "isArchived", "updateTime"}},
			{Action: "get", Help: "one article (id) with its content.", Path: "/v2/knowledgebase/article/{id}", Single: true},
			{Action: "folder", Help: "a folder and its contents; query folderId or folderPath, organizationId.", Path: "/v2/knowledgebase/folder", Single: true},
			{Action: "create", Help: "create articles; body [{name, content: {html}, organizationId (omit for global), destinationFolderId or destinationFolderPath}].", Method: "POST",
				Path: "/v2/knowledgebase/articles", Capability: "documentation", Body: true, Bulk: true},
			{Action: "update", Help: "update articles; body [{id, name, content}].", Method: "PATCH",
				Path: "/v2/knowledgebase/articles", Capability: "documentation", Body: true, Bulk: true},
			{Action: "archive", Help: "archive articles; body [id, ...]. Reversible with restore.", Method: "POST",
				Path: "/v2/knowledgebase/articles/archive", Capability: "documentation", Body: true, Bulk: true},
			{Action: "restore", Help: "restore archived articles; body [id, ...].", Method: "POST",
				Path: "/v2/knowledgebase/articles/restore", Capability: "documentation", Body: true, Bulk: true},
			{Action: "delete", Help: "permanently delete archived articles; body [id, ...].", Method: "POST",
				Path: "/v2/knowledgebase/articles/delete", Capability: "documentation", Body: true, Bulk: true, Destructive: true},
		}},

	// ticketing
	{Name: "ninjaone_ticket", Group: "ticketing", Title: "NinjaOne tickets",
		Description: "NinjaOne ticketing. Freshservice is the ticket system of record here; use these only when asked about NinjaOne tickets specifically. " +
			"There is no plain ticket list: list boards, then board_run one.",
		Views: []View{
			{Action: "get", Help: "one ticket (id), including its version.", Path: "/v2/ticketing/ticket/{id}", Single: true},
			{Action: "log_entries", Help: "comments and history of ticket id; query type.", Path: "/v2/ticketing/ticket/{id}/log-entry", Paging: Anchor,
				Brief: []string{"id", "type", "createTime", "publicEntry", "body", "appUserContactId", "timeTracked", "system"}},
			{Action: "boards", Help: "ticket boards (saved views).", Path: "/v2/ticketing/trigger/boards",
				Brief: []string{"id", "name", "description", "ticketCount", "system"}},
			{Action: "board_run", Help: "tickets on board id; body may hold filters, sortBy, searchCriteria, includeColumns.", Method: "POST",
				Path: "/v2/ticketing/trigger/board/{id}/run", Paging: Board, Items: "data"},
			{Action: "statuses", Help: "ticket statuses.", Path: "/v2/ticketing/statuses"},
			{Action: "attributes", Help: "ticket attributes (custom ticket fields).", Path: "/v2/ticketing/attributes"},
			{Action: "forms", Help: "ticket forms.", Path: "/v2/ticketing/ticket-form", Brief: []string{"id", "name", "description", "active", "default"}},
			{Action: "form", Help: "one ticket form (id).", Path: "/v2/ticketing/ticket-form/{id}", Single: true},
			{Action: "contacts", Help: "ticketing contacts.", Path: "/v2/ticketing/contact/contacts"},
			{Action: "create", Help: "create a ticket; body {clientId, ticketFormId, subject, description: {public, body}, status, priority, severity, type, nodeId, requesterUid, ...}.", Method: "POST",
				Path: "/v2/ticketing/ticket", Capability: "tickets", Body: true},
			{Action: "update", Help: "update ticket id; body must carry the version from get (a stale version is refused).", Method: "PUT",
				Path: "/v2/ticketing/ticket/{id}", Capability: "tickets", Body: true, Require: []string{"version"}},
			{Action: "comment", Help: "comment on ticket id; body {public: bool, body or htmlBody, timeTracked (seconds)}. A public comment can email the requester.", Method: "POST",
				Path: "/v2/ticketing/ticket/{id}/comment", Capability: "tickets", Body: true, Multipart: "comment", Require: []string{"public"}},
		}},

	// backup
	{Name: "ninjaone_backup", Group: "backup", Title: "Backup",
		Description: "NinjaOne Backup jobs and storage usage. Job filters go in query: sf (\"status in (FAILED,CANCELED)\"), ptf (\"planType in (IMAGE,FILE_FOLDER)\"), stf (\"startTime after 2026-09-01T00:00:00Z\"), include (active|deleted|all).",
		Views: []View{
			{Action: "jobs", Help: "backup jobs for devices matching df.", Path: "/v2/backup/jobs", Paging: Cursor, Items: "results", DF: true},
			{Action: "integrity_jobs", Help: "backup integrity-check jobs for devices matching df.", Path: "/v2/backup/integrity-check-jobs", Paging: Cursor, Items: "results", DF: true},
			{Action: "usage", Help: "backup storage per device; query includeDeletedDevices.", Path: "/v2/queries/backup/usage", Paging: Cursor, Items: "results"},
			{Action: "organization_usage", Help: "backup storage per location of organization id.", Path: "/v2/organization/{id}/locations/backup/usage"},
		}},
}
