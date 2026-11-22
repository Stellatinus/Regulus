import type { PermissionInfo } from "./api";

// The permission catalogue the hub lists (internal/auth/permissions.go).
export const CATALOGUE: PermissionInfo[] = [
  { id: "servers.read", description: "See the servers and their metrics" },
  { id: "servers.manage", description: "Add, rename and remove servers" },
  { id: "layout.edit", description: "Arrange the dashboard" },
  { id: "users.manage", description: "Manage accounts" },
  { id: "roles.manage", description: "Manage roles" },
  { id: "audit.read", description: "Read the audit log" },
  {
    id: "inventory.read",
    description:
      "See the hardware inventory: the disks of the servers with their serial numbers, and where a disk moved",
  },
  {
    id: "core.update",
    description: "Check for, download and choose how to update Regulus",
  },
];

export const ADMIN_PERMISSIONS = CATALOGUE.map((p) => p.id);
export const VIEWER_PERMISSIONS = ["servers.read", "layout.edit"];
