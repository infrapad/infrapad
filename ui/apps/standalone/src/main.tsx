import "@patternfly/react-core/dist/styles/base.css";
import "@infrapad/ui/styles.css";
import "./styles.css";

import { InfraPadDocuments, type InfraPadServices } from "@infrapad/ui";
import {
  Alert,
  Bullseye,
  Button,
  EmptyState,
  EmptyStateBody,
  Masthead,
  MastheadBrand,
  MastheadContent,
  MastheadMain,
  Page,
  PageSection,
  Spinner,
} from "@patternfly/react-core";
import { UserIcon } from "@patternfly/react-icons";
import { initializeAppearance } from "./appearance";
import { ThemeSettings } from "./ThemeSettings";
import { StrictMode, useCallback, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";

interface UIIdentity {
  username: string;
  email?: string;
}

interface UIConfig {
  identity: UIIdentity | null;
  services: InfraPadServices;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function parseConfig(value: unknown): UIConfig {
  if (!isRecord(value) || !isRecord(value.services)) {
    throw new Error("The UI configuration response is malformed.");
  }

  const { infrapadApiBaseUrl, prometheusApiBaseUrl } = value.services;
  if (
    typeof infrapadApiBaseUrl !== "string" ||
    infrapadApiBaseUrl.length === 0 ||
    typeof prometheusApiBaseUrl !== "string" ||
    prometheusApiBaseUrl.length === 0
  ) {
    throw new Error("The UI service configuration is malformed.");
  }

  let identity: UIIdentity | null;
  if (value.identity === null) {
    identity = null;
  } else if (
    isRecord(value.identity) &&
    typeof value.identity.username === "string" &&
    value.identity.username.length > 0 &&
    (value.identity.email === undefined || typeof value.identity.email === "string")
  ) {
    identity = {
      username: value.identity.username,
      ...(value.identity.email === undefined ? {} : { email: value.identity.email }),
    };
  } else {
    throw new Error("The UI identity configuration is malformed.");
  }

  return {
    identity,
    services: { infrapadApiBaseUrl, prometheusApiBaseUrl },
  };
}

async function loadConfig(signal: AbortSignal): Promise<UIConfig> {
  let response: Response;
  try {
    response = await fetch("/ui/config", {
      cache: "no-store",
      signal,
      headers: { Accept: "application/json" },
    });
  } catch (cause) {
    if (cause instanceof DOMException && cause.name === "AbortError") throw cause;
    throw new Error("Unable to connect to the UI configuration endpoint.");
  }

  if (!response.ok) {
    throw new Error(`The UI configuration endpoint returned ${response.status}.`);
  }

  let body: unknown;
  try {
    body = await response.json();
  } catch {
    throw new Error("The UI configuration endpoint returned invalid JSON.");
  }
  return parseConfig(body);
}

function LoadingPage() {
  return (
    <Bullseye className="standalone-full-page">
      <Spinner size="xl" aria-label="Loading InfraPad" />
    </Bullseye>
  );
}

function ConfigurationError({ message, retry }: { message: string; retry: () => void }) {
  return (
    <Bullseye className="standalone-full-page">
      <Alert variant="danger" title="InfraPad could not be configured" isInline>
        <p>{message}</p>
        <Button variant="link" isInline onClick={retry}>Retry</Button>
      </Alert>
    </Bullseye>
  );
}

function NotFound() {
  return (
    <PageSection>
      <EmptyState titleText="Page not found" headingLevel="h1">
        <EmptyStateBody>The requested InfraPad page does not exist.</EmptyStateBody>
        <Button component="a" href="/documents" variant="primary">View documents</Button>
      </EmptyState>
    </PageSection>
  );
}

function ReadyApplication({ config }: { config: UIConfig & { identity: UIIdentity } }) {
  const masthead = (
    <Masthead className="standalone-masthead">
      <MastheadMain>
        <MastheadBrand>
          <span className="standalone-brand">InfraPad</span>
        </MastheadBrand>
      </MastheadMain>
      <MastheadContent>
        <ThemeSettings />
        <div className="standalone-user" aria-label={`Signed in as ${config.identity.username}`}>
          <UserIcon aria-hidden="true" />
          <span>{config.identity.username}</span>
        </div>
      </MastheadContent>
    </Masthead>
  );

  return (
    <BrowserRouter>
      <Page masthead={masthead}>
        <Routes>
          <Route path="/" element={<Navigate replace to="/documents" />} />
          <Route path="/documents/*" element={<InfraPadDocuments services={config.services} />} />
          <Route path="*" element={<NotFound />} />
        </Routes>
      </Page>
    </BrowserRouter>
  );
}

function Bootstrap() {
  const [attempt, setAttempt] = useState(0);
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "error"; message: string }
    | { kind: "ready"; config: UIConfig & { identity: UIIdentity } }
  >({ kind: "loading" });

  useEffect(() => {
    const controller = new AbortController();
    setState({ kind: "loading" });
    void loadConfig(controller.signal)
      .then((config) => {
        if (config.identity === null) {
          const returnTo = `${window.location.pathname}${window.location.search}${window.location.hash}`;
          window.location.replace(`/auth?returnTo=${encodeURIComponent(returnTo)}`);
          return;
        }
        setState({ kind: "ready", config: { ...config, identity: config.identity } });
      })
      .catch((cause) => {
        if (controller.signal.aborted) return;
        setState({
          kind: "error",
          message: cause instanceof Error ? cause.message : "Unknown configuration error.",
        });
      });
    return () => controller.abort();
  }, [attempt]);

  const retry = useCallback(() => setAttempt((current) => current + 1), []);

  if (state.kind === "loading") return <LoadingPage />;
  if (state.kind === "error") return <ConfigurationError message={state.message} retry={retry} />;
  return <ReadyApplication config={state.config} />;
}

const root = document.getElementById("root");
if (!root) throw new Error("Missing application root element");

initializeAppearance();

createRoot(root).render(
  <StrictMode>
    <Bootstrap />
  </StrictMode>,
);
