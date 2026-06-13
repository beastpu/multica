/** Raw Swarm review state. Treated as an open string at the boundary so a new
 * Swarm value downgrades to a generic rendering rather than crashing. */
export type PerforceReviewState =
  | "needsReview"
  | "needsRevision"
  | "approved"
  | "rejected"
  | "archived";

export interface PerforceConnection {
  workspace_id: string;
  swarm_url: string;
  swarm_user: string;
  /** Whether a Swarm ticket/password is stored. The secret itself is never
   * returned to clients. */
  has_credential: boolean;
  last_polled_at?: string;
}

export interface GetPerforceConnectionResponse {
  connection: PerforceConnection | null;
  /** Whether the deployment has the at-rest key (MULTICA_PERFORCE_SECRET_KEY). */
  configured: boolean;
  /** Whether the caller may edit the connection. Older backends omit it; treat
   * absence as false for read-only safety. */
  can_manage?: boolean;
}

export interface PerforceReview {
  review_id: number;
  /** Raw Swarm state; render unknown values via a generic fallback. */
  state: string;
  title: string;
  html_url: string;
  author?: string;
  /** Pending changelist under review. */
  shelved_cl?: number;
  /** Submitted changelist once the review lands (renumbered from shelved_cl). */
  committed_cl?: number;
}
