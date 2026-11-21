import { HttpClient, HttpErrorResponse, HttpInterceptorFn } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';
import { MatSnackBar } from '@angular/material/snack-bar';
import { Observable, catchError, throwError } from 'rxjs';
import {
  BmpServer, Config, Global, InjectRequest, NeighborConfig, Policy, RibPage, RibQuery, Route, RpkiServer,
} from './models';

const base = '/api/v1';

/** Shows the server's reason for every failed API call. */
export const errorInterceptor: HttpInterceptorFn = (req, next) => {
  const snack = inject(MatSnackBar);
  return next(req).pipe(
    catchError((err: HttpErrorResponse) => {
      const reason = err.error?.error ?? (err.status === 0 ? 'The daemon is not reachable.' : err.message);
      snack.open(reason, 'Dismiss', { duration: 8000, panelClass: 'snack-error' });
      return throwError(() => err);
    }),
  );
};

@Injectable({ providedIn: 'root' })
export class Api {
  private http = inject(HttpClient);

  config = () => this.http.get<Config>(`${base}/config`);
  setGlobal = (g: Global) => this.http.put<void>(`${base}/global`, g);
  setPolicy = (p: Policy) => this.http.put<void>(`${base}/policy`, p);

  addNeighbor = (n: NeighborConfig) => this.http.post<void>(`${base}/neighbors`, n);
  updateNeighbor = (n: NeighborConfig) => this.http.put<void>(`${base}/neighbors/${encodeURIComponent(n.address)}`, n);
  deleteNeighbor = (addr: string) => this.http.delete<void>(`${base}/neighbors/${encodeURIComponent(addr)}`);
  neighborAction = (addr: string, action: string, body: { direction?: string; message?: string } = {}) =>
    this.http.post<void>(`${base}/neighbors/${encodeURIComponent(addr)}/${action}`, body);

  rib(q: RibQuery, offset: number, limit: number, refresh: boolean): Observable<RibPage> {
    const params: Record<string, string | number | boolean> = { ...q, offset, limit, refresh };
    return this.http.get<RibPage>(`${base}/rib`, { params });
  }

  addRoute = (r: Route) => this.http.post<void>(`${base}/routes`, r);
  deleteRoute = (prefix: string) => this.http.delete<void>(`${base}/routes`, { params: { prefix } });
  clearInjected = () => this.http.delete<void>(`${base}/routes/injected`);

  inject = (req: InjectRequest) => this.http.post<void>(`${base}/inject`, req);
  cancelInject = () => this.http.delete<void>(`${base}/inject`);
  /** Uploads a dump and injects it; the options part must come first. */
  injectUpload(req: InjectRequest, file: File) {
    const form = new FormData();
    form.append('options', JSON.stringify(req));
    form.append('file', file, file.name);
    return this.http.post<void>(`${base}/inject/upload`, form, { reportProgress: true, observe: 'events' });
  }

  rpki = () => this.http.get<RpkiServer[]>(`${base}/rpki`);
  addRpki = (s: { address: string; port: number }) => this.http.post<void>(`${base}/rpki`, s);
  deleteRpki = (s: { address: string; port: number }) => this.http.delete<void>(`${base}/rpki`, { params: { ...s } });
  addBmp = (s: BmpServer) => this.http.post<void>(`${base}/bmp`, s);
  deleteBmp = (s: BmpServer) =>
    this.http.delete<void>(`${base}/bmp`, { params: { address: s.address, port: s.port } });
}
