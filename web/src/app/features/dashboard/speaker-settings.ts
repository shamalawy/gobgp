import { ChangeDetectionStrategy, Component, inject, signal } from '@angular/core';
import { NonNullableFormBuilder, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { Api } from '../../core/api.service';
import { Global } from '../../core/models';
import { asn, errorText, ipv4Address, words } from '../../shared/validators';

@Component({
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [ReactiveFormsModule, MatDialogModule, MatButtonModule, MatFormFieldModule, MatInputModule],
  template: `
    <h2 mat-dialog-title>Speaker settings</h2>
    <mat-dialog-content>
      <p class="muted">
        Saving restarts the BGP engine: every session is reset and bulk-injected routes are dropped. Static routes and
        neighbors come back from the configuration.
      </p>
      <form [formGroup]="form" class="fields" id="speaker" (ngSubmit)="save()">
        <mat-form-field>
          <mat-label>Local AS number</mat-label>
          <input matInput formControlName="asn" inputmode="numeric" />
          <mat-error>{{ err('asn') }}</mat-error>
        </mat-form-field>
        <mat-form-field>
          <mat-label>Router ID</mat-label>
          <input matInput formControlName="routerId" class="mono" />
          <mat-error>{{ err('routerId') }}</mat-error>
        </mat-form-field>
        <mat-form-field>
          <mat-label>Listen port</mat-label>
          <input matInput type="number" formControlName="listenPort" />
          <mat-hint>-1 accepts no incoming sessions</mat-hint>
          <mat-error>{{ err('listenPort') }}</mat-error>
        </mat-form-field>
        <mat-form-field class="wide">
          <mat-label>Listen addresses</mat-label>
          <input matInput formControlName="listenAddresses" class="mono" />
          <mat-hint>Space separated; empty listens on all addresses</mat-hint>
        </mat-form-field>
        <mat-form-field>
          <mat-label>Confederation AS</mat-label>
          <input matInput formControlName="confedId" inputmode="numeric" />
          <mat-hint>RFC 5065; empty when not in a confederation</mat-hint>
          <mat-error>{{ err('confedId') }}</mat-error>
        </mat-form-field>
        <mat-form-field class="wide">
          <mat-label>Confederation member ASes</mat-label>
          <input matInput formControlName="confedMembers" />
          <mat-error>{{ err('confedMembers') }}</mat-error>
        </mat-form-field>
      </form>
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button matButton mat-dialog-close>Cancel</button>
      <button matButton="filled" form="speaker" [disabled]="saving()">Save and restart engine</button>
    </mat-dialog-actions>
  `,
})
export class SpeakerSettings {
  private api = inject(Api);
  private ref = inject(MatDialogRef<SpeakerSettings>);
  private global = inject<Global>(MAT_DIALOG_DATA);
  protected readonly saving = signal(false);

  protected readonly form = inject(NonNullableFormBuilder).group({
    asn: [String(this.global.asn), [Validators.required, asn]],
    routerId: [this.global.routerId, [Validators.required, ipv4Address]],
    listenPort: [this.global.listenPort, [Validators.required, Validators.min(-1), Validators.max(65535)]],
    listenAddresses: [(this.global.listenAddresses ?? []).join(' ')],
    confedId: [this.global.confederation ? String(this.global.confederation.id) : '', asn],
    confedMembers: [(this.global.confederation?.members ?? []).join(' ')],
  });

  protected err = (name: string) => errorText(this.form.get(name));

  protected save() {
    if (this.form.invalid) return this.form.markAllAsTouched();
    const v = this.form.getRawValue();
    const global: Global = {
      ...this.global,
      asn: +v.asn,
      routerId: v.routerId.trim(),
      listenPort: v.listenPort,
      listenAddresses: words(v.listenAddresses),
      confederation: v.confedId ? { id: +v.confedId, members: words(v.confedMembers).map(Number) } : undefined,
    };
    this.saving.set(true);
    this.api.setGlobal(global).subscribe({
      next: () => this.ref.close(true),
      error: () => this.saving.set(false),
    });
  }
}
