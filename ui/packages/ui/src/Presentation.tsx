// Host-owned declarative presentation primitives: fixed cards (title →
// Markdown → labeled fields → labeled links) and the shared sanitized
// Markdown renderer used for both card Markdown and fallback block bodies.
// Components accept presentation/Markdown data only, never
// integration-specific content schemas.

import {
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  List,
  ListItem,
  Title,
} from "@patternfly/react-core";
import DOMPurify from "dompurify";
import type { DOMPurify as DOMPurifyInstance } from "dompurify";
import { marked } from "marked";

import type { InfrapadBlock, InfrapadCard, InfrapadPresentation } from "./api.js";

/**
 * Renders a block through the host presentation path: nonempty
 * server-provided cards, otherwise the server-rendered Markdown body.
 */
export function BlockPresentation({ block }: { block: Pick<InfrapadBlock, "presentation" | "markdown"> }) {
  const cards = block.presentation?.cards ?? [];
  if (cards.length > 0) {
    return <PresentationCards presentation={{ cards }} />;
  }
  return <SafeMarkdown text={block.markdown ?? ""} />;
}

/** Renders presentation cards in their supplied order. */
export function PresentationCards({ presentation }: { presentation: InfrapadPresentation }) {
  return (
    <div className="infrapad-presentation">
      {(presentation.cards ?? []).map((card, i) => (
        <PresentationCard key={i} card={card} />
      ))}
    </div>
  );
}

/** One fixed-layout card; absent sections are omitted. */
export function PresentationCard({ card }: { card: InfrapadCard }) {
  const fields = card.fields ?? [];
  const links = card.links ?? [];
  return (
    <section className="infrapad-presentation-card">
      {card.title && (
        <Title headingLevel="h4" size="md" className="infrapad-presentation-card-title">
          {card.title}
        </Title>
      )}
      {card.markdown && <SafeMarkdown text={card.markdown} />}
      {fields.length > 0 && (
        <DescriptionList isHorizontal isCompact className="infrapad-presentation-fields">
          {fields.map((field, i) => (
            <DescriptionListGroup key={i}>
              <DescriptionListTerm>{field.label}</DescriptionListTerm>
              <DescriptionListDescription>{field.value}</DescriptionListDescription>
            </DescriptionListGroup>
          ))}
        </DescriptionList>
      )}
      {links.length > 0 && (
        <List isPlain className="infrapad-presentation-links">
          {links.map((link, i) => (
            <ListItem key={i}>
              <SafeLink label={link.label} url={link.url} />
            </ListItem>
          ))}
        </List>
      )}
    </section>
  );
}

/** Labeled link following the shared navigation policy. */
export function SafeLink({ label, url }: { label: string; url: string }) {
  const text = label || url;
  if (!isSafeHref(url)) {
    return <span className="infrapad-unsafe-link">{text}</span>;
  }
  return <a href={url}>{text}</a>;
}

/** Markdown rendered through the shared marked → DOMPurify pipeline. */
export function SafeMarkdown({ text }: { text: string }) {
  return (
    <div
      className="infrapad-markdown-content"
      dangerouslySetInnerHTML={{ __html: renderSafeMarkdown(text) }}
    />
  );
}

/** Converts Markdown to sanitized HTML with the shared link policy. */
export function renderSafeMarkdown(text: string): string {
  return sanitizer().sanitize(marked.parse(text, { async: false }) as string, {
    FORBID_TAGS: ["form"],
    FORBID_ATTR: ["action", "formaction", "target"],
  });
}

/**
 * Navigation policy shared by Markdown links and card links: only HTTP(S)
 * and relative destinations are allowed.
 */
export function isSafeHref(href: string): boolean {
  try {
    // Relative destinations resolve against an HTTP base; the URL parser
    // also normalizes whitespace/control-character scheme obfuscation.
    const url = new URL(href, "http://relative.invalid/");
    return url.protocol === "http:" || url.protocol === "https:";
  } catch {
    return false;
  }
}

// A dedicated DOMPurify instance keeps the link-policy hook from affecting
// other sanitizer users.
let purifier: DOMPurifyInstance | undefined;

function sanitizer(): DOMPurifyInstance {
  if (!purifier) {
    purifier = DOMPurify(window);
    purifier.addHook("afterSanitizeAttributes", (node) => {
      const tag = node.nodeName.toLowerCase();
      if (tag !== "a" && tag !== "area") return;
      for (const attr of ["href", "xlink:href"]) {
        const href = node.getAttribute(attr);
        if (href !== null && !isSafeHref(href)) {
          // Unsafe destinations stay visible as non-clickable text.
          node.removeAttribute(attr);
          node.setAttribute("class", "infrapad-unsafe-link");
        }
      }
    });
  }
  return purifier;
}
