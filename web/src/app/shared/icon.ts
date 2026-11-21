import { ChangeDetectionStrategy, Component, computed, input } from '@angular/core';

// Stroked line icons on a 24px grid, inlined so the single binary needs no
// icon font.
const circle = (cx: number, cy: number, r: number) => `M${cx - r} ${cy}a${r} ${r} 0 1 0 ${2 * r} 0a${r} ${r} 0 1 0-${2 * r} 0`;
const PATHS = {
  dashboard: 'M3 3h7v9H3zM14 3h7v5h-7zM14 12h7v9h-7zM3 16h7v5H3z',
  neighbors: `M9 12h6${circle(6, 12, 3)}${circle(18, 12, 3)}`,
  rib: 'M3 5h18v14H3zM3 10h18M3 14.5h18M9 5v14',
  inject: 'M12 16V4M7 9l5-5 5 5M4 16v4h16v-4',
  policy: 'M3 6h18M6 12h12M10 18h4',
  shield: 'M12 3l8 3v6c0 4.5-3.2 8-8 9-4.8-1-8-4.5-8-9V6z',
  sun: `${circle(12, 12, 4)}M12 2v2M12 20v2M2 12h2M20 12h2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4`,
  moon: 'M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5z',
  add: 'M12 5v14M5 12h14',
  more: `${circle(12, 5, 0.5)}${circle(12, 12, 0.5)}${circle(12, 19, 0.5)}`,
  refresh: 'M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8M21 3v5h-5',
  delete: 'M3 6h18M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2M10 11v6M14 11v6',
  edit: 'M4 20h4L19 9l-4-4L4 16zM13.5 6.5l4 4',
  settings: `M4 7h10M18 7h2M4 17h2M10 17h10${circle(16, 7, 2)}${circle(8, 17, 2)}`,
  menu: 'M4 6h16M4 12h16M4 18h16',
} as const;

export type IconName = keyof typeof PATHS;

@Component({
  selector: 'app-icon',
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `<svg viewBox="0 0 24 24" aria-hidden="true"><path [attr.d]="path()" /></svg>`,
  styles: `
    :host {
      display: inline-flex;
      width: 20px;
      height: 20px;
      flex: none;
    }
    svg {
      fill: none;
      stroke: currentColor;
      stroke-width: 2;
      stroke-linecap: round;
      stroke-linejoin: round;
    }
  `,
})
export class Icon {
  readonly name = input.required<IconName>();
  protected readonly path = computed(() => PATHS[this.name()]);
}
