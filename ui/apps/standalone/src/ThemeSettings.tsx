import {
  MenuToggle,
  Popover,
  Title,
  ToggleGroup,
  ToggleGroupItem,
} from "@patternfly/react-core";
import { PaletteIcon } from "@patternfly/react-icons";
import { useState } from "react";
import {
  appearance,
  setAppearance,
  type Appearance,
} from "./appearance";

export function ThemeSettings() {
  const [open, setOpen] = useState(false);
  const [selected, setSelected] = useState<Appearance>({ ...appearance });

  function choose<K extends keyof Appearance>(key: K, value: Appearance[K]) {
    setAppearance(key, value);
    setSelected((current) => ({ ...current, [key]: value }));
  }

  const body = (
    <div className="standalone-theme-options">
      <section aria-labelledby="standalone-theme-label">
        <Title id="standalone-theme-label" headingLevel="h3" size="md">Theme</Title>
        <ToggleGroup isCompact aria-label="Theme">
          {([ ["default", "Default"], ["felt", "Project Felt"] ] as const).map(([value, label]) => (
            <ToggleGroupItem key={value} text={label} isSelected={selected.theme === value}
              onChange={() => choose("theme", value)} />
          ))}
        </ToggleGroup>
      </section>
      <section aria-labelledby="standalone-scheme-label">
        <Title id="standalone-scheme-label" headingLevel="h3" size="md">Color scheme</Title>
        <ToggleGroup isCompact aria-label="Color scheme">
          {([ ["system", "System"], ["light", "Light"], ["dark", "Dark"] ] as const).map(([value, label]) => (
            <ToggleGroupItem key={value} text={label} isSelected={selected.colorScheme === value}
              onChange={() => choose("colorScheme", value)} />
          ))}
        </ToggleGroup>
      </section>
      <section aria-labelledby="standalone-contrast-label">
        <Title id="standalone-contrast-label" headingLevel="h3" size="md">Contrast mode</Title>
        <ToggleGroup isCompact aria-label="Contrast mode">
          {([ ["system", "System"], ["default", "Default"], ["high-contrast", "High contrast"], ["glass", "Glass"] ] as const).map(([value, label]) => (
            <ToggleGroupItem key={value} text={label} isSelected={selected.contrastMode === value}
              onChange={() => choose("contrastMode", value)} />
          ))}
        </ToggleGroup>
      </section>
    </div>
  );

  return (
    <Popover headerContent="Appearance" headerComponent="h2" bodyContent={body}
      position="bottom-end" maxWidth="min(25rem, calc(100vw - 2rem))"
      isVisible={open} shouldClose={() => setOpen(false)} shouldOpen={() => setOpen(true)}>
      <MenuToggle variant="plain" aria-label="Theme settings" aria-expanded={open} isExpanded={open}>
        <PaletteIcon aria-hidden="true" />
      </MenuToggle>
    </Popover>
  );
}
