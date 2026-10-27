// Signs in through the real login form, then loads one page with constant concurrency.
// Env: BASE (app URL), PAGE (scenario name), OUT (summary file).
import http from "k6/http";
import { check } from "k6";

const BASE = __ENV.BASE;
const PAGES = {
  lista: { path: "/" },
  dia: { path: "/current_accounts?date=2025-06-10" },
  dashboard: { path: "/dashboard?month=2025-02" },
  json: { path: "/current_accounts.json" },
  criar: { path: "/current_accounts", post: true },
};
const page = PAGES[__ENV.PAGE];

export const options = {
  scenarios: {
    warmup: { executor: "constant-vus", vus: 20, duration: "10s", gracefulStop: "0s", tags: { phase: "warmup" } },
    load: { executor: "constant-vus", vus: 20, duration: "30s", startTime: "10s", gracefulStop: "5s", tags: { phase: "load" } },
  },
  thresholds: {
    "http_req_failed{phase:load}": ["rate<0.01"],
    "http_req_duration{phase:load}": ["p(95)>=0"],
    "http_reqs{phase:load}": ["count>=0"],
  },
  summaryTrendStats: ["avg", "p(50)", "p(95)", "p(99)", "max"],
};

// Cookies are kept by hand: Rails marks them Secure (assume_ssl) and the benchmark talks plain HTTP.
// Laravel stores its session in a cookie, so the cookie captured at login keeps the CSRF token.
function remember(jar, res) {
  for (const [name, list] of Object.entries(res.cookies)) jar[name] = list[0].value;
}
function cookieHeader(jar) {
  return Object.entries(jar).map(([k, v]) => `${k}=${v}`).join("; ");
}
// The last token on the page belongs to the main form. Rails uses per-form tokens, and the first one on
// every page is the "Sair" (logout) button in the header.
function csrf(html) {
  const all = [...html.matchAll(/name="(authenticity_token|csrfmiddlewaretoken|_token)" value="([^"]+)"/g)];
  const m = all[all.length - 1];
  return m ? { name: m[1], value: m[2] } : null;
}

export function setup() {
  const jar = {};
  const form = http.get(`${BASE}/session/new`);
  remember(jar, form);
  const token = csrf(form.body);
  const login = http.post(`${BASE}/session`,
    { email_address: "benchmark@exemplo.com", password: "benchmark123", [token.name]: token.value },
    { headers: { Cookie: cookieHeader(jar), Referer: `${BASE}/session/new` }, redirects: 0 });
  remember(jar, login);
  if (!jar.session_id) throw new Error(`login failed: ${login.status}`);
  const newForm = http.get(`${BASE}/current_accounts/new`, { headers: { Cookie: cookieHeader(jar) } });
  remember(jar, newForm);
  return { cookie: cookieHeader(jar), token: csrf(newForm.body) };
}

export default function (data) {
  const params = { headers: { Cookie: data.cookie, Referer: `${BASE}/current_accounts/new` }, redirects: 0 };
  let res;
  if (page.post) {
    res = http.post(`${BASE}${page.path}`, {
      [data.token.name]: data.token.value,
      "current_account[type_launche]": "credito", "current_account[price]": "123.45",
      "current_account[date_ocurrence]": "2031-01-01", "current_account[cost_id]": "6",
      "current_account[cash_account_id]": "5", "current_account[historic]": "benchmark",
    }, params);
    check(res, { "created (302)": (r) => r.status === 302 });
  } else {
    res = http.get(`${BASE}${page.path}`, params);
    check(res, { "ok (200)": (r) => r.status === 200 });
  }
}

export function handleSummary(summary) {
  return { [__ENV.OUT]: JSON.stringify(summary, null, 2) };
}
