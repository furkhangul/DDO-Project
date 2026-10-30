import http from "k6/http";
import { check } from "k6";
import { Rate } from "k6/metrics";

const base = `${__ENV.K6_API || "http://localhost:8080"}/api/v1`;
const unexpected = new Rate("unexpected_errors");

export const options = {
  scenarios: {
    browse: { executor: "constant-vus", vus: 30, duration: "45s", exec: "browse" },
    book: { executor: "constant-arrival-rate", rate: 40, timeUnit: "1s", duration: "45s", preAllocatedVUs: 60, maxVUs: 120, exec: "book" },
  },
  thresholds: {
    unexpected_errors: ["rate<0.01"],
    "http_req_duration{name:availability}": ["p(95)<300"],
    "http_req_duration{name:book}": ["p(95)<800"],
  },
  summaryTrendStats: ["med", "p(95)", "p(99)", "max"],
};

export function setup() {
  const response = http.post(`${base}/auth/login`, JSON.stringify({ email: "rezervasyon@konakpro.test", password: "Konak!2026" }), {
    headers: { "Content-Type": "application/json" },
  });
  const health = http.get(`${__ENV.K6_API || "http://localhost:8080"}/health`).json();
  return { token: response.json("tokens.access_token"), today: health.business_date };
}

const shift = (iso, days) => {
  const date = new Date(`${iso}T00:00:00Z`);
  date.setUTCDate(date.getUTCDate() + days);
  return date.toISOString().slice(0, 10);
};

const auth = (data) => ({ Authorization: `Bearer ${data.token}`, "Content-Type": "application/json" });

export function browse(data) {
  const from = shift(data.today, Math.floor(Math.random() * 60));
  const availability = http.get(`${base}/availability?from=${from}&to=${shift(from, 7)}`, { headers: auth(data), tags: { name: "availability" } });
  unexpected.add(availability.status !== 200);
  const quote = http.post(
    `${base}/quotes`,
    JSON.stringify({ room_type_id: 1, rate_plan_id: 1, check_in: from, check_out: shift(from, 3), adults: 2 }),
    { headers: auth(data), tags: { name: "quote" } },
  );
  unexpected.add(quote.status !== 200 && quote.status !== 422);
  const list = http.get(`${base}/reservations?limit=25`, { headers: auth(data), tags: { name: "list" } });
  unexpected.add(list.status !== 200);
}

export function book(data) {
  const from = shift(data.today, 150 + Math.floor(Math.random() * 120));
  const response = http.post(
    `${base}/reservations`,
    JSON.stringify({
      room_type_id: 1 + Math.floor(Math.random() * 4),
      rate_plan_id: 1,
      check_in: from,
      check_out: shift(from, 2),
      adults: 2,
      guest: { full_name: "Yük Testi" },
    }),
    { headers: auth(data), tags: { name: "book" } },
  );
  check(response, { "kabul veya dolu": (r) => r.status === 201 || r.status === 409 });
  unexpected.add(response.status !== 201 && response.status !== 409 && response.status !== 422);
}
