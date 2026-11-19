import { TARGET_METHOD } from "./types";
import type { PolicyInput, TargetInput, TargetType } from "./types";

// Client-side validation mirroring internal/usecase/policy/validate.go and
// the policies DTO checks. The server re-validates everything; these errors
// exist to fail fast with the same rules.

export function validateTarget(t: TargetInput): string[] {
  const errors: string[] = [];
  if (t.selector.trim() === "") {
    errors.push("Selector is required.");
  }
  const hasCred = (t.credential_ref ?? "").trim() !== "";
  const hasPatterns = (t.include ?? []).length > 0 || (t.exclude ?? []).length > 0;

  if (hasPatterns && t.type !== "host_filesystem") {
    errors.push("Include/exclude patterns are only supported for host filesystem targets.");
  }
  for (const p of [...(t.include ?? []), ...(t.exclude ?? [])]) {
    if (p.trim() === "") errors.push("Include/exclude patterns must not be blank.");
  }

  const options = t.options ?? {};
  const boolOption = (key: string): void => {
    const v = options[key];
    if (v !== undefined && v !== "true" && v !== "false") {
      errors.push(`Option ${key} must be true or false.`);
    }
  };

  switch (t.type) {
    case "host_filesystem":
      if (t.selector.trim() !== "" && !t.selector.startsWith("/")) {
        errors.push("Host filesystem selector must be an absolute path.");
      }
      if (hasCred) errors.push("Host filesystem targets take no credential reference.");
      {
        const v = options["symlink"];
        if (v !== undefined && v !== "follow" && v !== "skip" && v !== "record") {
          errors.push("Host option symlink must be follow, skip, or record.");
        }
      }
      boolOption("one_file_system");
      break;
    case "docker_config":
      if (hasCred) errors.push("Docker config targets take no credential reference.");
      boolOption("include_compose");
      break;
    case "docker_volume":
      if (hasCred) errors.push("Docker volume targets take no credential reference.");
      break;
    case "postgres":
    case "mysql": {
      const dbErr = checkDBName(t.selector);
      if (dbErr !== undefined) errors.push(dbErr);
      if (!hasCred) errors.push(`Database target requires a credential-profile reference.`);
      const v = options["scope"];
      if (v !== undefined && v !== "database" && v !== "cluster") {
        errors.push("Database option scope must be database or cluster.");
      }
      break;
    }
    case "mssql": {
      const dbErr = checkDBName(t.selector);
      if (dbErr !== undefined) errors.push(dbErr);
      if (!hasCred) errors.push(`Database target requires a credential-profile reference.`);
      const v = options["backup_type"];
      if (v !== undefined && v !== "full") {
        errors.push(`MSSQL backup_type must be "full".`);
      }
      break;
    }
    case "redis": {
      const v = options["persistence"];
      if (v !== undefined && v !== "rdb" && v !== "aof" && v !== "both") {
        errors.push("Redis option persistence must be rdb, aof, or both.");
      }
      boolOption("verify");
      break;
    }
    case "app_stack":
      if (hasCred) errors.push("App-stack targets take no credential reference.");
      break;
  }
  return errors;
}

function checkDBName(name: string): string | undefined {
  const n = name.trim();
  if (n === "") return undefined; // covered by the required check
  if (n !== name) return `Database name must not have leading/trailing whitespace.`;
  if (n.includes("/") || /[\s]/.test(n)) {
    return `Database name must be a plain identifier, not a path.`;
  }
  return undefined;
}

export function validatePolicy(p: PolicyInput): string[] {
  const errors: string[] = [];
  if (p.name.trim() === "") errors.push("Policy name is required.");
  if (p.agent_ids.length === 0) errors.push("Assign at least one agent.");
  if (p.agent_ids.some((a) => a.trim() === "")) errors.push("Agent assignments must not be blank.");
  if (p.targets.length === 0) errors.push("Add at least one target.");
  p.targets.forEach((t, i) => {
    for (const e of validateTarget(t)) errors.push(`Target ${i + 1}: ${e}`);
  });

  const { keep_last_n, keep_days, keep_minimum } = p.retention;
  if (keep_last_n === 0 && keep_days === 0) {
    errors.push("Retention needs at least one bound (keep last N or keep days).");
  }
  if (keep_last_n < 0 || keep_days < 0 || keep_minimum < 0) {
    errors.push("Retention values must be non-negative.");
  }
  if (p.encryption.enabled && p.encryption.key_ref.trim() === "") {
    errors.push("Encryption key reference is required when encryption is enabled.");
  }
  const algo = p.compression.algorithm.trim().toLowerCase();
  if (algo !== "" && algo !== "zstd") {
    errors.push(`Unsupported compression algorithm — only "zstd" is supported.`);
  }
  if (p.compression.level < 0 || p.compression.level > 22) {
    errors.push("Compression level must be 0–22 (0 = default).");
  }
  return errors;
}

/** Fill fixed per-type methods before submit (server would default them). */
export function withMethods(targets: TargetInput[]): TargetInput[] {
  return targets.map((t) => ({ ...t, method: TARGET_METHOD[t.type as TargetType] ?? "full" }));
}
