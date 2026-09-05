-- Add "api_parameters" to "audit_events" (schema/tables/audit_events.sql),
-- the API-type sibling of "cli_parameters"/"mcp_tool_parameters": a place for
-- a caller-supplied justification behind an API action that needs one, such
-- as a merge-queue gate override. Nullable and unpopulated by ordinary API
-- audit events, exactly like its CLI/MCP siblings.
ALTER TABLE "audit_events" ADD COLUMN "api_parameters" text;
