import { ChangeDetectionStrategy, Component, computed, input } from '@angular/core';

/** A filled line chart of evenly spaced samples, scaled to its own maximum. */
@Component({
  selector: 'app-sparkline',
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <svg [attr.viewBox]="'0 0 ' + width + ' ' + height" preserveAspectRatio="none" role="img" [attr.aria-label]="label()">
      <path class="area" [attr.d]="paths().area" />
      <path class="line" [attr.d]="paths().line" />
    </svg>
  `,
  styles: `
    :host {
      display: block;
      height: 120px;
    }
    svg {
      width: 100%;
      height: 100%;
      overflow: visible;
    }
    .line {
      fill: none;
      stroke: var(--primary);
      stroke-width: 1.5;
      vector-effect: non-scaling-stroke;
    }
    .area {
      fill: var(--primary);
      opacity: 0.14;
    }
  `,
})
export class Sparkline {
  readonly values = input.required<number[]>();
  readonly capacity = input(120); // samples that fill the full width
  readonly label = input('');

  protected readonly width = 600;
  protected readonly height = 120;
  protected readonly paths = computed(() => {
    const v = this.values();
    if (v.length < 2) return { line: '', area: '' };
    const max = Math.max(...v, 1);
    const step = this.width / (this.capacity() - 1);
    const pts = v.map((y, i) => `${(i * step).toFixed(1)},${(this.height - (y / max) * (this.height - 4)).toFixed(1)}`);
    const line = 'M' + pts.join('L');
    return { line, area: `${line}L${((v.length - 1) * step).toFixed(1)},${this.height}L0,${this.height}Z` };
  });
}
