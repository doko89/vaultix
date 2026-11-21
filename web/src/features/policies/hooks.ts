import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { ID } from "@/shared/types";
import { errorText } from "@/shared/lib/api";
import { useToasts } from "@/shared/ui/toast";
import {
  addTarget,
  createPolicy,
  createSchedule,
  deletePolicy,
  deleteSchedule,
  deleteTarget,
  getPolicy,
  getVersion,
  listAgentOptions,
  listCredentialOptions,
  listPolicies,
  listSchedules,
  listStorageOptions,
  listTargets,
  listVersions,
  pauseSchedule,
  publishVersion,
  resumeSchedule,
  runSchedule,
  setPolicyEnabled,
  updatePolicy,
  updateSchedule,
  updateTarget,
} from "./api";
import type { PublishVersionInput } from "./api";
import type { PolicyInput, ScheduleInput, TargetInput } from "./types";

export const POLICIES_KEY = ["policies"] as const;
export const SCHEDULES_KEY = ["schedules"] as const;

function invalidatePolicy(queryClient: ReturnType<typeof useQueryClient>) {
  void queryClient.invalidateQueries({ queryKey: [...POLICIES_KEY] });
}

export function usePolicies(
  opts: { q?: string | undefined; limit?: number | undefined; offset?: number | undefined } = {},
) {
  const { q, limit, offset } = opts;
  return useQuery({
    queryKey: [...POLICIES_KEY, "list", q ?? "", limit ?? 25, offset ?? 0],
    queryFn: () => listPolicies({ q, limit, offset }),
  });
}

export function usePolicy(policyId: ID | undefined) {
  return useQuery({
    queryKey: [...POLICIES_KEY, "detail", policyId ?? ""],
    queryFn: () => {
      if (policyId === undefined) throw new Error("policy id is required");
      return getPolicy(policyId);
    },
    enabled: policyId !== undefined,
  });
}

export function usePolicyVersions(policyId: ID | undefined) {
  return useQuery({
    queryKey: [...POLICIES_KEY, "versions", policyId ?? ""],
    queryFn: () => {
      if (policyId === undefined) throw new Error("policy id is required");
      return listVersions(policyId);
    },
    enabled: policyId !== undefined,
  });
}

export function usePolicyVersion(policyId: ID | undefined, version: number | undefined) {
  return useQuery({
    queryKey: [...POLICIES_KEY, "version", policyId ?? "", version ?? 0],
    queryFn: () => {
      if (policyId === undefined || version === undefined) throw new Error("policy version is required");
      return getVersion(policyId, version);
    },
    enabled: policyId !== undefined && version !== undefined,
  });
}

export function usePolicyTargets(policyId: ID | undefined) {
  return useQuery({
    queryKey: [...POLICIES_KEY, "targets", policyId ?? ""],
    queryFn: () => {
      if (policyId === undefined) throw new Error("policy id is required");
      return listTargets(policyId);
    },
    enabled: policyId !== undefined,
  });
}

export function useCreatePolicy(onDone: () => void) {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: (input: PolicyInput) => createPolicy(input),
    onSuccess: (res) => {
      push({ tone: "success", title: `Policy “${res.policy.name}” created` });
      invalidatePolicy(queryClient);
      onDone();
    },
    onError: (err) => push({ tone: "error", title: "Could not create policy", message: errorText(err) }),
  });
}

export function useUpdatePolicy(onDone: () => void) {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: ({ policyId, input }: { policyId: ID; input: PolicyInput }) => updatePolicy(policyId, input),
    onSuccess: (res) => {
      push({ tone: "success", title: `Policy “${res.policy.name}” updated (v${res.version.version})` });
      invalidatePolicy(queryClient);
      onDone();
    },
    onError: (err) => push({ tone: "error", title: "Could not update policy", message: errorText(err) }),
  });
}

export function useDeletePolicy() {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: (policyId: ID) => deletePolicy(policyId),
    onSuccess: () => {
      push({ tone: "success", title: "Policy deleted" });
      invalidatePolicy(queryClient);
    },
    onError: (err) => push({ tone: "error", title: "Could not delete policy", message: errorText(err) }),
  });
}

export function useSetPolicyEnabled() {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: ({ policyId, enabled }: { policyId: ID; enabled: boolean }) => setPolicyEnabled(policyId, enabled),
    onSuccess: (policy) => {
      push({ tone: "success", title: policy.enabled ? "Policy enabled" : "Policy disabled" });
      invalidatePolicy(queryClient);
    },
    onError: (err) => push({ tone: "error", title: "Could not update policy", message: errorText(err) }),
  });
}

export function usePublishVersion(policyId: ID, onDone: () => void) {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: (input: PublishVersionInput) => publishVersion(policyId, input),
    onSuccess: (version) => {
      push({ tone: "success", title: `Published version ${version.version}` });
      invalidatePolicy(queryClient);
      onDone();
    },
    onError: (err) => push({ tone: "error", title: "Could not publish version", message: errorText(err) }),
  });
}

export function useTargetMutations(policyId: ID, onDone: () => void) {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  const done = () => {
    invalidatePolicy(queryClient);
    onDone();
  };
  const add = useMutation({
    mutationFn: (input: TargetInput) => addTarget(policyId, input),
    onSuccess: (v) => {
      push({ tone: "success", title: `Target added (now v${v.version})` });
      done();
    },
    onError: (err) => push({ tone: "error", title: "Could not add target", message: errorText(err) }),
  });
  const update = useMutation({
    mutationFn: ({ targetId, input }: { targetId: ID; input: TargetInput }) => updateTarget(policyId, targetId, input),
    onSuccess: (v) => {
      push({ tone: "success", title: `Target updated (now v${v.version})` });
      done();
    },
    onError: (err) => push({ tone: "error", title: "Could not update target", message: errorText(err) }),
  });
  const remove = useMutation({
    mutationFn: (targetId: ID) => deleteTarget(policyId, targetId),
    onSuccess: (v) => {
      push({ tone: "success", title: `Target removed (now v${v.version})` });
      invalidatePolicy(queryClient);
    },
    onError: (err) => push({ tone: "error", title: "Could not remove target", message: errorText(err) }),
  });
  return { add, update, remove };
}

// --- Schedules ---

export function useSchedules(policyId?: ID) {
  return useQuery({
    queryKey: [...SCHEDULES_KEY, "list", policyId ?? ""],
    queryFn: () => listSchedules(policyId),
  });
}

function invalidateSchedules(queryClient: ReturnType<typeof useQueryClient>) {
  void queryClient.invalidateQueries({ queryKey: [...SCHEDULES_KEY] });
}

export function useCreateSchedule(onDone: () => void) {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: (input: ScheduleInput) => createSchedule(input),
    onSuccess: () => {
      push({ tone: "success", title: "Schedule created" });
      invalidateSchedules(queryClient);
      onDone();
    },
    onError: (err) => push({ tone: "error", title: "Could not create schedule", message: errorText(err) }),
  });
}

export function useUpdateSchedule(onDone: () => void) {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: ({ scheduleId, input }: { scheduleId: ID; input: ScheduleInput }) => updateSchedule(scheduleId, input),
    onSuccess: () => {
      push({ tone: "success", title: "Schedule updated" });
      invalidateSchedules(queryClient);
      onDone();
    },
    onError: (err) => push({ tone: "error", title: "Could not update schedule", message: errorText(err) }),
  });
}

export function useDeleteSchedule() {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: (scheduleId: ID) => deleteSchedule(scheduleId),
    onSuccess: () => {
      push({ tone: "success", title: "Schedule deleted" });
      invalidateSchedules(queryClient);
    },
    onError: (err) => push({ tone: "error", title: "Could not delete schedule", message: errorText(err) }),
  });
}

export function usePauseResumeSchedule() {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: ({ scheduleId, paused }: { scheduleId: ID; paused: boolean }) =>
      paused ? pauseSchedule(scheduleId) : resumeSchedule(scheduleId),
    onSuccess: (schedule) => {
      push({ tone: "success", title: schedule.paused ? "Schedule paused" : "Schedule resumed" });
      invalidateSchedules(queryClient);
    },
    onError: (err) => push({ tone: "error", title: "Could not update schedule", message: errorText(err) }),
  });
}

export function useRunSchedule() {
  const queryClient = useQueryClient();
  const { push } = useToasts();
  return useMutation({
    mutationFn: ({ scheduleId, agentId }: { scheduleId: ID; agentId?: string | undefined }) =>
      runSchedule(scheduleId, agentId),
    onSuccess: (res) => {
      push({ tone: "success", title: `Manual run started (${res.jobs.length} job${res.jobs.length === 1 ? "" : "s"})` });
      invalidateSchedules(queryClient);
      void queryClient.invalidateQueries({ queryKey: ["jobs"] });
    },
    onError: (err) => push({ tone: "error", title: "Could not start manual run", message: errorText(err) }),
  });
}

// --- Reference data ---

export function useAgentOptions() {
  return useQuery({ queryKey: [...POLICIES_KEY, "agent-options"], queryFn: listAgentOptions, staleTime: 60_000 });
}

export function useStorageOptions() {
  return useQuery({ queryKey: [...POLICIES_KEY, "storage-options"], queryFn: listStorageOptions, staleTime: 60_000 });
}

export function useCredentialOptions() {
  return useQuery({ queryKey: [...POLICIES_KEY, "credential-options"], queryFn: listCredentialOptions, staleTime: 60_000 });
}
