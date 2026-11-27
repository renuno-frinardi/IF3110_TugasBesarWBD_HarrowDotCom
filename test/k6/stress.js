import http from "k6/http";
import { check, fail, sleep } from "k6";
import exec from "k6/execution";
import { Counter, Gauge, Rate, Trend } from "k6/metrics";

const baseURL = (__ENV.BASE_URL || "http://localhost:5173").replace(/\/+$/, "");
if (!/^https?:\/\/(localhost|127\.0\.0\.1)(:\d+)?$/i.test(baseURL)) {
  throw new Error("Stress tests are restricted to localhost.");
}

const testCase = __ENV.K6_CASE || "same_database";
if (testCase !== "same_database" && testCase !== "different_databases") {
  throw new Error("K6_CASE must be same_database or different_databases.");
}

const fixturePath = __ENV.K6_FIXTURES || "./fixtures.local.json";
const fixtureData = JSON.parse(open(fixturePath));
const jobs = fixtureData[testCase];
if (!Array.isArray(jobs) || jobs.length === 0) {
  throw new Error(`Fixture ${testCase} must contain approved migration jobs.`);
}

const migrationIDs = new Set();
const databaseIDs = new Set();
for (const job of jobs) {
  if (!Number.isInteger(job.database_id) || job.database_id < 1 ||
      !Number.isInteger(job.migration_id) || job.migration_id < 1) {
    throw new Error("Fixture jobs require positive integer database_id and migration_id.");
  }
  if (migrationIDs.has(job.migration_id)) {
    throw new Error(`Migration ${job.migration_id} is repeated; fixtures must be one-use.`);
  }
  migrationIDs.add(job.migration_id);
  databaseIDs.add(job.database_id);
}
if (testCase === "same_database" && databaseIDs.size !== 1) {
  throw new Error("same_database fixtures must all use one database.");
}
if (testCase === "different_databases" && databaseIDs.size < 2) {
  throw new Error("different_databases fixtures must span at least two databases.");
}

const normalVUs = Number(__ENV.K6_NORMAL_VUS || 2);
const stageSeconds = Number(__ENV.K6_STAGE_SECONDS || 30);
const apiP95Limit = Number(__ENV.API_P95_MS || 1000);
const apiErrorLimit = Number(__ENV.API_ERROR_RATE_LIMIT || 0.01);
const jobSuccessMinimum = Number(__ENV.JOB_SUCCESS_RATE_MIN || 0.95);
const pollIntervalSeconds = Number(__ENV.POLL_INTERVAL_SECONDS || 1);
const jobTimeoutMs = Number(__ENV.JOB_TIMEOUT_MS || 600000);

if (!Number.isInteger(normalVUs) || normalVUs < 1 ||
    !Number.isInteger(stageSeconds) || stageSeconds < 1 ||
    !Number.isFinite(apiP95Limit) || apiP95Limit <= 0 ||
    !Number.isFinite(apiErrorLimit) || apiErrorLimit < 0 || apiErrorLimit > 1 ||
    !Number.isFinite(jobSuccessMinimum) || jobSuccessMinimum < 0 || jobSuccessMinimum > 1 ||
    !Number.isFinite(pollIntervalSeconds) || pollIntervalSeconds <= 0 ||
    !Number.isFinite(jobTimeoutMs) || jobTimeoutMs <= 0) {
  throw new Error("Invalid k6 load, threshold, or polling configuration.");
}

const apiRequestCount = new Counter("api_requests_measured");
const apiResponseTime = new Trend("api_response_time", true);
const apiErrorRate = new Rate("api_error_rate");
const jobSubmissionCount = new Counter("jobs_accepted");
const jobConflictCount = new Counter("job_conflicts");
const jobConflictRate = new Rate("job_conflict_rate");
const jobTerminalCount = new Counter("jobs_terminal");
const jobSuccessCount = new Counter("jobs_succeeded");
const jobFailureCount = new Counter("jobs_failed");
const jobSuccessRate = new Rate("job_success_rate");
const queueWaitTime = new Trend("queue_wait_ms", true);
const jobCompletionTime = new Trend("job_completion_ms", true);
const jobExecutionTime = new Trend("job_execution_ms", true);
const jobTimeoutCount = new Counter("job_timeouts");
const jobPollErrorCount = new Counter("job_poll_errors");
const fixtureExhaustedCount = new Counter("fixture_exhausted");
const jobTimestampErrorCount = new Counter("job_timestamp_errors");

const stageDuration = new Gauge("phase_window_seconds");
const phases = [
  ["ramp_to_normal", 1], ["normal", 1],
  ["ramp_to_2x", 2], ["2x", 2],
  ["ramp_to_4x", 4], ["4x", 4],
  ["ramp_down_to_2x", 2], ["recovery_2x", 2],
  ["ramp_down_to_normal", 1], ["recovery_normal", 1],
  ["drain", 0],
];

export const options = {
  scenarios: {
    migration_stress: {
      executor: "ramping-vus",
      startVUs: 0,
      stages: phases.map(([, multiplier]) => ({ duration: `${stageSeconds}s`, target: normalVUs * multiplier })),
      gracefulRampDown: "10m",
      gracefulStop: "10m",
    },
  },
  summaryTrendStats: ["avg", "med", "p(90)", "p(95)", "p(99)", "max"],
  thresholds: {
    api_response_time: [`p(95)<${apiP95Limit}`],
    api_error_rate: [`rate<${apiErrorLimit}`],
    job_success_rate: [`rate>${jobSuccessMinimum}`],
    jobs_terminal: ["count>0"],
    job_timeouts: ["count==0"],
    job_timestamp_errors: ["count==0"],
    jobs_accepted: ["count>0"],
    fixture_exhausted: ["count==0"],
  },
};

export function setup() {
  for (const [level] of phases) {
    stageDuration.add(stageSeconds, { load_level: level });
  }
}

function loadLevel(time = Date.now()) {
  const index = Math.max(0, Math.floor((time - exec.scenario.startTime) / (stageSeconds * 1000)));
  return index < phases.length ? phases[index][0] : "after_drain";
}

function recordApi(response, ok, endpoint, level) {
  const tags = { endpoint, load_level: level };
  apiRequestCount.add(1, tags);
  apiResponseTime.add(response.timings.duration, tags);
  apiErrorRate.add(ok ? 0 : 1, tags);
  check(response, { [`${endpoint} response is valid`]: () => ok });
}

function parseJSON(response) {
  try {
    return response.json();
  } catch (_) {
    return null;
  }
}

function recordDuration(metric, start, end, tags) {
  const startMs = Date.parse(start);
  const endMs = Date.parse(end);
  if (!Number.isFinite(startMs) || !Number.isFinite(endMs) || endMs < startMs) {
    jobTimestampErrorCount.add(1, tags);
    return;
  }
  metric.add(endMs - startMs, tags);
}

let csrfToken = "";
let sessionCookieName = "";
let sessionCookie = "";
function authenticate() {
  if (!__ENV.K6_USERNAME || !__ENV.K6_PASSWORD) {
    throw new Error("Set K6_USERNAME and K6_PASSWORD in the environment.");
  }

  const loginLevel = loadLevel();
  const login = http.post(
    `${baseURL}/api/login`,
    { username: __ENV.K6_USERNAME, password: __ENV.K6_PASSWORD },
    { headers: { Accept: "application/json" }, tags: { name: "login" } },
  );
  recordApi(login, login.status === 200, "login", loginLevel);
  if (login.status !== 200) fail(`Login failed with HTTP ${login.status}.`);

  const sessionLevel = loadLevel();
  const session = http.get(`${baseURL}/api/session`, {
    headers: { Accept: "application/json" },
    tags: { name: "session" },
  });
  const sessionData = parseJSON(session);
  const validSession = session.status === 200 && sessionData && sessionData.csrf_token;
  recordApi(session, Boolean(validSession), "session", sessionLevel);
  if (!validSession) fail(`Session validation failed with HTTP ${session.status}.`);
  csrfToken = sessionData.csrf_token;

  const cookies = http.cookieJar().cookiesForURL(baseURL);
  sessionCookieName = Object.keys(cookies)[0] || "";
  sessionCookie = sessionCookieName ? cookies[sessionCookieName][0] : "";
  if (!sessionCookie) fail("Login response did not set a session cookie.");
}

function terminalJob(jobData) {
  return ["success", "failed", "partial", "partially_reverted"].includes(jobData.status);
}

export default function () {
  if (!csrfToken) authenticate();
  sleep(1);

  const level = loadLevel();
  const fixtureIndex = exec.scenario.iterationInTest;
  const job = jobs[fixtureIndex];
  if (!job) {
    fixtureExhaustedCount.add(1, { load_level: level });
    exec.test.abort(`No unused fixture for iteration ${fixtureIndex}; add approved test migrations.`);
  }

  const apiHeaders = { Accept: "application/json" };
  const [listResponse, submitResponse] = http.batch([
    {
      method: "GET",
      url: `${baseURL}/api/v2/databases/${job.database_id}/migrations`,
      params: {
        headers: apiHeaders,
        cookies: { [sessionCookieName]: sessionCookie },
        tags: { name: "migration_list" },
      },
    },
    {
      method: "POST",
      url: `${baseURL}/api/v2/migrations/${job.migration_id}/execute`,
      params: {
        headers: { ...apiHeaders, "X-CSRF-Token": csrfToken },
        cookies: { [sessionCookieName]: sessionCookie },
        tags: { name: "migration_execute" },
      },
    },
  ]);

  const listData = parseJSON(listResponse);
  const listOK = listResponse.status === 200 && listData && Array.isArray(listData.items);
  recordApi(listResponse, Boolean(listOK), "migration_list", level);

  const submitData = submitResponse.status === 202 ? parseJSON(submitResponse) : null;
  const accepted = submitResponse.status === 202 && submitData && submitData.job_id;
  const conflict = submitResponse.status === 409;
  recordApi(submitResponse, Boolean(accepted), "migration_execute", level);
  jobConflictRate.add(conflict ? 1 : 0, { load_level: level });
  if (conflict) {
    jobConflictCount.add(1, { load_level: level });
    sleep(1);
    return;
  }
  if (!accepted) return;

  const jobId = submitData.job_id;
  jobSubmissionCount.add(1, { load_level: level });
  const submittedAt = Date.now();

  while (Date.now() - submittedAt < jobTimeoutMs) {
    const pollLevel = loadLevel();
    const response = http.get(`${baseURL}/api/v2/jobs/${encodeURIComponent(jobId)}`, {
      headers: apiHeaders,
      cookies: { [sessionCookieName]: sessionCookie },
      tags: { name: "job_status" },
    });
    const jobData = parseJSON(response);
    const validResponse = response.status === 200 && jobData && jobData.status;
    recordApi(response, Boolean(validResponse), "job_status", pollLevel);
    if (!validResponse) {
      jobPollErrorCount.add(1, { load_level: pollLevel });
      sleep(pollIntervalSeconds);
      continue;
    }

    if (terminalJob(jobData)) {
      const finished = Date.parse(jobData.finished_at);
      const jobTags = { load_level: loadLevel(Number.isFinite(finished) ? finished : Date.now()) };
      recordDuration(queueWaitTime, jobData.created_at, jobData.started_at, jobTags);
      jobTerminalCount.add(1, jobTags);
      if (jobData.status === "success") {
        jobSuccessCount.add(1, jobTags);
        jobSuccessRate.add(true, jobTags);
      } else {
        jobFailureCount.add(1, jobTags);
        jobSuccessRate.add(false, jobTags);
      }

      if (jobData.created_at && jobData.finished_at) {
        recordDuration(jobCompletionTime, jobData.created_at, jobData.finished_at, jobTags);
      } else {
        jobTimestampErrorCount.add(1, jobTags);
      }
      if (jobData.started_at && jobData.finished_at) {
        recordDuration(jobExecutionTime, jobData.started_at, jobData.finished_at, jobTags);
      } else {
        jobTimestampErrorCount.add(1, jobTags);
      }
      return;
    }
    sleep(pollIntervalSeconds);
  }

  const jobTags = { load_level: loadLevel() };
  jobSuccessRate.add(false, jobTags);
  jobTimeoutCount.add(1, jobTags);
}
