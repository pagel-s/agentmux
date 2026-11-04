"use strict";

// Pure presentation of evidence, shared by the browser and direct Node tests.
// This module has no DOM, network, task-state or recovery authority. A timestamp
// describes reporting; it never upgrades a retained receipt into a live outcome.
(function (root, factory) {
  const observation = factory();
  if (typeof module === "object" && module.exports) module.exports = observation;
  else root.AgentmuxObservation = observation;
})(globalThis, function () {
  const eventKinds = new Set([
    "native_event", "message", "tool", "result", "usage", "error", "other",
  ]);
  const unknownWorker = "Worker reporting unknown; no check-in for this attempt. Observations reset on broker restart.";

  function observedTime(value, now) {
    if (typeof value !== "string" || value === "") return null;
    const at = Date.parse(value);
    return Number.isFinite(at) && at <= now + 1000 ? at : null;
  }

  function observationTimeText(value, options = {}) {
    const now = options.now ?? Date.now(), at = observedTime(value, now);
    const format = options.formatTime || ((time) => new Date(time).toLocaleTimeString());
    return at === null ? "Observation time unknown" : "Observed " + format(at);
  }

  function nativeReportingText(status, options = {}) {
    const now = options.now ?? Date.now(), at = observedTime(status?.readiness_observed_at, now);
    if (at === null)
      return "Adapter reporting unknown; no valid readiness observation recorded.";
    const format = options.formatDate || ((time) => new Date(time).toLocaleString());
    const last = format(at);
    if (status.connected !== true)
      return "Adapter not reporting; last readiness " + last + ". Retained delivery outcome is not confirmed by this observation.";
    return "Adapter reporting; readiness observed " + last + ".";
  }

  function taskProgressText(progress, options = {}) {
    if (!progress) return unknownWorker;
    const now = options.now ?? Date.now();
    const worker = observedTime(progress.worker_observed_at, now) === null
      ? "unknown" : progress.worker_observed_at;
    const count = progress.event_count;
    if (!Number.isInteger(count) || count < 0 || count > 1000000)
      return "Worker check-in: " + worker + ". Native event count unknown. Worker reporting does not establish task correctness.";
    const event = observedTime(progress.event_observed_at, now) === null
      ? "unknown" : progress.event_observed_at;
    const kind = eventKinds.has(progress.event_kind) ? progress.event_kind : "unknown";
    return "Worker check-in: " + worker + " · " + count + " native events" +
      (count > 0 ? " · last " + kind + " at " + event : " observed") +
      ". Worker reporting and native events do not establish task correctness.";
  }

  function nativePendingCountText(status) {
    if (status?.pending_known === false) return "unknown";
    const count = status?.pending_count ?? (Array.isArray(status?.pending) ? status.pending.length : null);
    return Number.isInteger(count) && count >= 0
      ? String(count) + (status.pending_more ? "+" : "") : "unknown";
  }

  return Object.freeze({ observationTimeText, nativeReportingText, taskProgressText, nativePendingCountText });
});
