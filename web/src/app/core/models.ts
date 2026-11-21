// Types of the daemon's REST and event API. They mirror the Go structs in
// internal/config and internal/bgp.

export type Family = 'ipv4' | 'ipv6';
export type FsmState = 'Idle' | 'Connect' | 'Active' | 'OpenSent' | 'OpenConfirm' | 'Established';
export const FSM_STATES: FsmState[] = ['Idle', 'Connect', 'Active', 'OpenSent', 'OpenConfirm', 'Established'];

export interface PolicyRef {
  policies?: string[];
  default?: '' | 'accept' | 'reject';
}

export interface Global {
  asn: number;
  routerId: string;
  listenPort: number;
  listenAddresses?: string[];
  confederation?: { id: number; members: number[] };
  import?: PolicyRef;
  export?: PolicyRef;
}

export interface NeighborConfig {
  address: string;
  remoteAsn: number;
  localAsn?: number;
  description?: string;
  adminDown?: boolean;
  families?: string[];
  passive?: boolean;
  localAddress?: string;
  remotePort?: number;
  holdTime?: number;
  keepalive?: number;
  connectRetry?: number;
  password?: string;
  tcpAo?: { sendId: number; receiveId: number; algorithm: string; key: string };
  ebgpMultihop?: number;
  ttlSecurityMin?: number;
  routeReflectorClient?: boolean;
  clusterId?: string;
  routeServerClient?: boolean;
  addPathReceive?: boolean;
  addPathSendMax?: number;
  gracefulRestart?: {
    restartTime?: number;
    helperOnly?: boolean;
    notification?: boolean;
    longLived?: boolean;
    llgrTime?: number;
  };
  bfd?: { minTx?: number; minRx?: number; multiplier?: number; multihop?: boolean };
  removePrivateAs?: '' | 'all' | 'replace';
  allowOwnAs?: number;
  maxPrefixes?: number;
  import?: PolicyRef;
  export?: PolicyRef;
}

export interface Neighbor extends NeighborConfig {
  state: FsmState;
  since: number;
  peerRouterId?: string;
  counts: Partial<Record<Family, { received: number; advertised: number }>>;
  capabilities: string[];
  messagesIn: number;
  messagesOut: number;
  flaps: number;
  lastError?: string;
  bfdState?: string;
}

export interface RibCounts {
  adjIn: number;
  locRib: number;
  prefixes: number;
  adjOut: number;
}

export interface Status {
  asn: number;
  routerId: string;
  listenPort: number;
  started: number;
  ribs: Record<Family, RibCounts>;
  neighbors: Neighbor[];
  established: number;
  version: string;
  runtime: {
    goroutines: number;
    heapAlloc: number;
    sys: number;
    gcCycles: number;
    cpuPercent: number;
    cpus: number;
  };
}

export interface FsmEvent {
  time: string;
  peer: string;
  asn: number;
  state: FsmState;
  reason?: string;
}

export interface Route {
  prefix: string;
  nextHop?: string;
  asPath?: string;
  origin?: string;
  med?: number;
  localPref?: number;
  communities?: string[];
  extCommunities?: string[];
  largeCommunities?: string[];
}

export interface RibRow extends Route {
  best: boolean;
  stale?: boolean;
  peer?: string;
  peerAsn?: number;
  age: number;
  pathId?: number;
  rpki?: string;
}

export interface RibPage {
  total: number;
  size: number;
  snapshot: number;
  bestOnly: boolean;
  rows: RibRow[];
}

export interface RibQuery {
  table: 'global' | 'adj-in' | 'adj-out';
  neighbor: string;
  family: Family;
  prefix: string;
  match: string;
  nextHop: string;
  asPath: string;
  community: string;
  best: string;
}

export interface InjectRequest {
  source: string;
  format: string;
  rate: number;
  limit: number;
  family: string;
  peerAs: number;
  peerIp: string;
  nextHopSelf: boolean;
  nextHop: string;
  prepend: number;
  localPref: number | null;
  med: number | null;
  communities: string[];
}

export interface InjectStatus {
  state: 'idle' | 'running' | 'done' | 'failed' | 'cancelled';
  source: string;
  started: number;
  finished: number;
  bytesRead: number;
  bytesTotal: number;
  routes: number;
  failed: number;
  attrSets: number;
  rate: number;
  error?: string;
}

export interface PrefixMatch {
  prefix: string;
  ge?: number;
  le?: number;
}
export interface PrefixSet {
  name: string;
  prefixes: PrefixMatch[];
}
export interface NamedSet {
  name: string;
  list: string[];
}
export interface SetRef {
  name: string;
  match?: string;
}
export interface CommunityAction {
  op: 'add' | 'remove' | 'replace';
  values: string[];
}
export interface Statement {
  match: {
    prefixSet?: SetRef;
    asPathSet?: SetRef;
    communitySet?: SetRef;
    extCommunitySet?: SetRef;
    largeCommunitySet?: SetRef;
    rpki?: string;
  };
  set: {
    med?: { op: 'set' | 'add' | 'sub'; value: number };
    localPref?: number;
    asPrepend?: { asn: number; repeat: number };
    nextHop?: string;
    communities?: CommunityAction;
    extCommunities?: CommunityAction;
    largeCommunities?: CommunityAction;
  };
  action?: '' | 'accept' | 'reject';
}
export interface RouteMap {
  name: string;
  statements: Statement[];
}
export interface Policy {
  prefixSets: PrefixSet[];
  asPathSets: NamedSet[];
  communitySets: NamedSet[];
  extCommunitySets: NamedSet[];
  largeCommunitySets: NamedSet[];
  routeMaps: RouteMap[];
}

export interface RpkiServer {
  address: string;
  port: number;
  up: boolean;
  since: number;
  serial: number;
  roasV4: number;
  roasV6: number;
}
export interface BmpServer {
  address: string;
  port: number;
  policy?: string;
}

export interface Config {
  global: Global;
  neighbors: NeighborConfig[];
  policy: Policy;
  routes: Route[];
  rpki: { address: string; port: number }[];
  bmp: BmpServer[];
}
