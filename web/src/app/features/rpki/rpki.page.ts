import { ChangeDetectionStrategy, Component, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { NonNullableFormBuilder, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatSelectModule } from '@angular/material/select';
import { startWith, switchMap, timer } from 'rxjs';
import { Api } from '../../core/api.service';
import { Clock } from '../../core/clock.service';
import { BmpServer, RpkiServer } from '../../core/models';
import { CountPipe, SincePipe } from '../../shared/format';
import { Icon } from '../../shared/icon';
import { errorText, ipAddress } from '../../shared/validators';

const hostValidators = [Validators.required, Validators.pattern(/^[A-Za-z0-9.:_-]+$/)];
const portValidators = [Validators.required, Validators.min(1), Validators.max(65535)];

@Component({
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [ReactiveFormsModule, MatButtonModule, MatFormFieldModule, MatInputModule, MatSelectModule, Icon, CountPipe, SincePipe],
  template: `
    <div class="page">
      <div class="page-head">
        <div>
          <h1>RPKI and BMP</h1>
          <p>
            RPKI caches feed route origin validation over RTR; route maps can then match on the result. BMP stations
            receive a monitoring copy of the tables.
          </p>
        </div>
      </div>

      <section class="panel">
        <div class="panel-head"><h2>RPKI cache servers</h2></div>
        @if (rpki().length) {
          <div class="scroll-x">
            <table class="data">
              <thead>
                <tr>
                  <th>Cache</th><th>Session</th><th>For</th><th class="num">Serial</th>
                  <th class="num">IPv4 ROAs</th><th class="num">IPv6 ROAs</th><th></th>
                </tr>
              </thead>
              <tbody>
                @for (s of rpki(); track s.address + s.port) {
                  <tr>
                    <td class="mono">{{ s.address }}:{{ s.port }}</td>
                    <td><strong [class]="s.up ? 'up' : 'down'">{{ s.up ? 'Connected' : 'Down' }}</strong></td>
                    <td class="mono">{{ s.since | since: clock.now() }}</td>
                    <td class="num mono">{{ s.serial }}</td>
                    <td class="num mono">{{ s.roasV4 | count }}</td>
                    <td class="num mono">{{ s.roasV6 | count }}</td>
                    <td class="num">
                      <button matIconButton (click)="removeRpki(s)" [attr.aria-label]="'Remove ' + s.address">
                        <app-icon name="delete" />
                      </button>
                    </td>
                  </tr>
                }
              </tbody>
            </table>
          </div>
        } @else {
          <p class="empty">No cache configured. Routes validate as "not found" until one is connected.</p>
        }
        <form class="panel-body add" [formGroup]="rpkiForm" (ngSubmit)="addRpki()">
          <mat-form-field>
            <mat-label>Cache address</mat-label>
            <input matInput formControlName="address" class="mono" placeholder="192.0.2.53" />
            <mat-hint>An IP address</mat-hint>
            <mat-error>{{ err(rpkiForm.controls.address) }}</mat-error>
          </mat-form-field>
          <mat-form-field>
            <mat-label>RTR port</mat-label>
            <input matInput type="number" formControlName="port" />
            <mat-error>{{ err(rpkiForm.controls.port) }}</mat-error>
          </mat-form-field>
          <button matButton="filled">Add cache</button>
        </form>
      </section>

      <section class="panel">
        <div class="panel-head"><h2>BMP monitoring stations</h2></div>
        @if (bmp().length) {
          <table class="data">
            <thead>
              <tr><th>Station</th><th>Routes monitored</th><th></th></tr>
            </thead>
            <tbody>
              @for (s of bmp(); track s.address + s.port) {
                <tr>
                  <td class="mono">{{ s.address }}:{{ s.port }}</td>
                  <td>{{ policyLabel(s.policy) }}</td>
                  <td class="num">
                    <button matIconButton (click)="removeBmp(s)" [attr.aria-label]="'Remove ' + s.address">
                      <app-icon name="delete" />
                    </button>
                  </td>
                </tr>
              }
            </tbody>
          </table>
        } @else {
          <p class="empty">No station configured.</p>
        }
        <form class="panel-body add" [formGroup]="bmpForm" (ngSubmit)="addBmp()">
          <mat-form-field>
            <mat-label>Station address</mat-label>
            <input matInput formControlName="address" class="mono" />
            <mat-error>{{ err(bmpForm.controls.address) }}</mat-error>
          </mat-form-field>
          <mat-form-field>
            <mat-label>Port</mat-label>
            <input matInput type="number" formControlName="port" />
            <mat-error>{{ err(bmpForm.controls.port) }}</mat-error>
          </mat-form-field>
          <mat-form-field>
            <mat-label>Routes to monitor</mat-label>
            <mat-select formControlName="policy">
              @for (p of policies; track p.value) {
                <mat-option [value]="p.value">{{ p.label }}</mat-option>
              }
            </mat-select>
          </mat-form-field>
          <button matButton="filled">Add station</button>
        </form>
      </section>
    </div>
  `,
  styles: `
    .add {
      display: flex;
      flex-wrap: wrap;
      gap: 12px;
      align-items: flex-start;
      border-top: 1px solid var(--line);
    }
    .add button {
      margin-top: 4px;
    }
    .up {
      color: var(--up);
    }
    .down {
      color: var(--down);
    }
  `,
})
export default class RpkiPage {
  private api = inject(Api);
  private fb = inject(NonNullableFormBuilder);
  protected readonly clock = inject(Clock);
  protected readonly rpki = signal<RpkiServer[]>([]);
  protected readonly bmp = signal<BmpServer[]>([]);
  protected readonly policies = [
    { value: 'pre', label: 'Adj-RIB-In before policy' },
    { value: 'post', label: 'Adj-RIB-In after policy' },
    { value: 'both', label: 'Adj-RIB-In before and after policy' },
    { value: 'local', label: 'Loc-RIB (RFC 9069)' },
    { value: 'all', label: 'Everything' },
  ];

  protected readonly rpkiForm = this.fb.group({
    address: ['', [Validators.required, ipAddress]],
    port: [3323, portValidators],
  });
  protected readonly bmpForm = this.fb.group({ address: ['', hostValidators], port: [11019, portValidators], policy: ['pre'] });

  constructor() {
    // Cache sessions have no event stream, so their state is polled.
    timer(0, 5000)
      .pipe(switchMap(() => this.api.rpki()), takeUntilDestroyed())
      .subscribe((list) => this.rpki.set(list));
    this.loadBmp();
  }

  protected err = errorText;
  protected policyLabel = (p?: string) => this.policies.find((x) => x.value === (p || 'pre'))?.label;

  private loadRpki = () => this.api.rpki().subscribe((list) => this.rpki.set(list));
  private loadBmp = () => this.api.config().subscribe((cfg) => this.bmp.set(cfg.bmp));

  protected addRpki() {
    if (this.rpkiForm.invalid) return this.rpkiForm.markAllAsTouched();
    this.api.addRpki(this.rpkiForm.getRawValue()).subscribe(() => {
      this.rpkiForm.reset();
      this.loadRpki();
    });
  }

  protected removeRpki(s: RpkiServer) {
    this.api.deleteRpki({ address: s.address, port: s.port }).subscribe(this.loadRpki);
  }

  protected addBmp() {
    if (this.bmpForm.invalid) return this.bmpForm.markAllAsTouched();
    this.api.addBmp(this.bmpForm.getRawValue()).subscribe(() => {
      this.bmpForm.reset();
      this.loadBmp();
    });
  }

  protected removeBmp(s: BmpServer) {
    this.api.deleteBmp(s).subscribe(this.loadBmp);
  }
}
