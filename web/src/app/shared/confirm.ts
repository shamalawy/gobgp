import { ChangeDetectionStrategy, Component, inject } from '@angular/core';
import { FormControl, ReactiveFormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialog, MatDialogModule } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { Observable, filter } from 'rxjs';

export interface ConfirmOptions {
  title: string;
  message: string;
  action: string; // the label of the confirming button; it names what happens
  /** Asks for an optional text, e.g. a shutdown message for the peer. */
  inputLabel?: string;
}

@Component({
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [ReactiveFormsModule, MatDialogModule, MatButtonModule, MatFormFieldModule, MatInputModule],
  template: `
    <h2 mat-dialog-title>{{ data.title }}</h2>
    <mat-dialog-content>
      <p>{{ data.message }}</p>
      @if (data.inputLabel) {
        <mat-form-field style="width: 100%">
          <mat-label>{{ data.inputLabel }}</mat-label>
          <input matInput [formControl]="text" maxlength="128" />
        </mat-form-field>
      }
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button matButton mat-dialog-close>Cancel</button>
      <button matButton="filled" [mat-dialog-close]="{ text: text.value }">{{ data.action }}</button>
    </mat-dialog-actions>
  `,
})
class ConfirmDialog {
  protected readonly data = inject<ConfirmOptions>(MAT_DIALOG_DATA);
  protected readonly text = new FormControl('', { nonNullable: true });
}

/** Opens a confirmation; the result emits only when the user confirms. */
export function confirm(dialog: MatDialog, options: ConfirmOptions): Observable<{ text: string }> {
  return dialog
    .open<ConfirmDialog, ConfirmOptions, { text: string }>(ConfirmDialog, { data: options, width: '460px' })
    .afterClosed()
    .pipe(filter((r): r is { text: string } => !!r));
}
