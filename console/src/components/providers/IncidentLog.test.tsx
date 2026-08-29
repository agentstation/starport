// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";

import type { ProviderIncidentLog } from "@/lib/api";

import { IncidentLog, incidentTiming } from "./IncidentLog";

afterEach(() => {
  cleanup();
});

const hourMs = 60 * 60 * 1000;

function iso(msAgo: number): string {
  return new Date(Date.now() - msAgo).toISOString();
}

function availableReport(
  incidents: NonNullable<ProviderIncidentLog["log"]["incidents"]>,
): ProviderIncidentLog {
  return {
    provider_id: "openai",
    log: { availability: "available", incidents, fetched_at: iso(0) },
  };
}

// --- Timing prose

test("phrases incident timing from the stated timestamps", () => {
  expect(incidentTiming(undefined, undefined)).toBe("");
  expect(incidentTiming(iso(2 * hourMs), undefined)).toBe(
    "started 2h ago · ongoing",
  );
  expect(incidentTiming(iso(3 * hourMs), iso(hourMs))).toBe(
    "started 3h ago · lasted 2h",
  );
  // A close time without a start still says the incident ended.
  expect(incidentTiming(undefined, iso(hourMs))).toBe("resolved");
  // A zero-value Go time is an absent stamp, not the first century.
  expect(incidentTiming("0001-01-01T00:00:00Z", undefined)).toBe("");
});

// --- The provider's published log

test("renders a published incident with severity, link, update, and components", () => {
  render(
    <IncidentLog
      name="OpenAI"
      statusPageUrl={undefined}
      failed={false}
      report={availableReport([
        {
          title: "Elevated error rates",
          indicator: "major",
          status: "investigating",
          started_at: iso(2 * hourMs),
          url: "https://status.openai.com/incidents/abc",
          update: "We are investigating elevated errors.",
          components: ["API", "ChatGPT"],
        },
        {
          title: "Feed notice without a stated severity",
        },
      ])}
    />,
  );
  const entries = screen.getAllByTestId("incident-entry");
  expect(entries).toHaveLength(2);
  expect(screen.getByText("major")).toBeTruthy();
  const link = screen.getByRole("link", { name: /Elevated error rates/ });
  expect(link.getAttribute("href")).toBe(
    "https://status.openai.com/incidents/abc",
  );
  expect(screen.getByText("started 2h ago · ongoing · investigating")).toBeTruthy();
  expect(screen.getByText("We are investigating elevated errors.")).toBeTruthy();
  expect(screen.getByText("API")).toBeTruthy();
  expect(screen.getByText("ChatGPT")).toBeTruthy();
  // The unstated entry gets a neutral chip and plain text, no link.
  expect(screen.getByText("incident")).toBeTruthy();
  expect(
    screen.queryByRole("link", { name: /Feed notice/ }),
  ).toBeNull();
});

test("folds the log past five entries behind one control", () => {
  const incidents = Array.from({ length: 8 }, (_, index) => ({
    title: `Incident ${index}`,
    started_at: iso((index + 1) * hourMs),
  }));
  render(
    <IncidentLog
      name="OpenAI"
      statusPageUrl={undefined}
      failed={false}
      report={availableReport(incidents)}
    />,
  );
  expect(screen.getAllByTestId("incident-entry")).toHaveLength(5);
  fireEvent.click(screen.getByRole("button", { name: "Show all 8" }));
  expect(screen.getAllByTestId("incident-entry")).toHaveLength(8);
  expect(screen.queryByRole("button", { name: /Show all/ })).toBeNull();
});

test("an answered empty log reads as a clean quarter", () => {
  render(
    <IncidentLog
      name="OpenAI"
      statusPageUrl={undefined}
      failed={false}
      report={availableReport([])}
    />,
  );
  expect(
    screen.getByText("No incidents reported in the last 90 days."),
  ).toBeTruthy();
});

// --- Honest states for logs the gateway cannot read

test("an unpublished log names the provider and points at its status page", () => {
  render(
    <IncidentLog
      name="Ollama"
      statusPageUrl="https://status.ollama.com"
      failed={false}
      report={{
        provider_id: "ollama",
        log: { availability: "unpublished" },
      }}
    />,
  );
  expect(
    screen.getByText(/Ollama does not publish a machine-readable incident log/),
  ).toBeTruthy();
  const link = screen.getByRole("link", { name: /Check its status page/ });
  expect(link.getAttribute("href")).toBe("https://status.ollama.com");
});

test("an unpublished log without a status page offers no dead link", () => {
  render(
    <IncidentLog
      name="Ollama"
      statusPageUrl={undefined}
      failed={false}
      report={{
        provider_id: "ollama",
        log: { availability: "unpublished" },
      }}
    />,
  );
  expect(screen.queryByRole("link")).toBeNull();
});

test("an unreachable log says so instead of implying a clean record", () => {
  render(
    <IncidentLog
      name="OpenAI"
      statusPageUrl={undefined}
      failed={false}
      report={{
        provider_id: "openai",
        log: { availability: "unreachable" },
      }}
    />,
  );
  expect(
    screen.getByText("The provider's incident log did not answer just now."),
  ).toBeTruthy();
});

test("a failed read renders an error state, not an empty log", () => {
  render(
    <IncidentLog
      name="OpenAI"
      statusPageUrl={undefined}
      failed={true}
      report={undefined}
    />,
  );
  expect(
    screen.getByText("The incident record could not be read just now."),
  ).toBeTruthy();
});

test("renders nothing before the first answer arrives", () => {
  render(
    <IncidentLog
      name="OpenAI"
      statusPageUrl={undefined}
      failed={false}
      report={undefined}
    />,
  );
  expect(screen.queryByTestId("incident-log")).toBeNull();
});

// --- The gateway's own record

test("observed transitions keep their provenance and read recovery as success", () => {
  render(
    <IncidentLog
      name="OpenAI"
      statusPageUrl={undefined}
      failed={false}
      report={{
        provider_id: "openai",
        log: { availability: "available", incidents: [] },
        observed: [
          {
            provider_id: "openai",
            indicator: "none",
            observed_at: iso(hourMs),
          },
          {
            provider_id: "openai",
            indicator: "major",
            description: "Elevated error rates",
            observed_at: iso(2 * hourMs),
          },
        ],
      }}
    />,
  );
  expect(screen.getByTestId("observed-transitions")).toBeTruthy();
  expect(screen.getByText("Observed by this gateway")).toBeTruthy();
  expect(screen.getByText("cleared")).toBeTruthy();
  expect(
    screen.getByText("The provider stopped reporting an incident."),
  ).toBeTruthy();
  expect(screen.getByText("major")).toBeTruthy();
  expect(screen.getByText("Elevated error rates")).toBeTruthy();
});
