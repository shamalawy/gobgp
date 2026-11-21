import { ChangeDetectionStrategy, Component, computed, input } from '@angular/core';
import { FSM_STATES, FsmState } from '../core/models';

/**
 * A neighbor's session state as its position in the BGP finite state machine:
 * six steps from Idle to Established, filled up to the current one.
 */
@Component({
  selector: 'app-fsm-state',
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <span class="steps" aria-hidden="true">
      @for (s of states; track s; let i = $index) {
        <i [class.on]="i <= index()"></i>
      }
    </span>
    <span class="label">{{ adminDown() ? 'Shut down' : state() }}</span>
  `,
  host: { '[class]': 'tone()', '[attr.title]': 'hint()' },
  styles: `
    :host {
      display: inline-flex;
      align-items: center;
      gap: 8px;
      --tone: var(--wait);
    }
    :host(.up) {
      --tone: var(--up);
    }
    :host(.down) {
      --tone: var(--down);
    }
    :host(.off) {
      --tone: var(--muted);
    }
    .steps {
      display: inline-flex;
      gap: 2px;
    }
    i {
      width: 5px;
      height: 14px;
      border-radius: 1px;
      background: var(--line);
    }
    i.on {
      background: var(--tone);
    }
    .label {
      color: var(--tone);
      font-weight: 500;
    }
  `,
})
export class FsmStateBadge {
  readonly state = input.required<FsmState>();
  readonly adminDown = input(false);

  protected readonly states = FSM_STATES;
  protected readonly index = computed(() => (this.adminDown() ? -1 : FSM_STATES.indexOf(this.state())));
  protected readonly tone = computed(() => {
    if (this.adminDown()) return 'off';
    if (this.state() === 'Established') return 'up';
    return this.state() === 'Idle' ? 'down' : 'wait';
  });
  protected readonly hint = computed(() =>
    this.adminDown() ? 'Administratively shut down' : `Step ${this.index() + 1} of 6: ${FSM_STATES.join(' → ')}`,
  );
}
