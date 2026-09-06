type PlaybackOwner = { pause(): void }
let owner: PlaybackOwner | undefined
export function claimRecordingPlayback(next: PlaybackOwner): void {
  if (owner && owner !== next) owner.pause()
  owner = next
}
export function releaseRecordingPlayback(current: PlaybackOwner): void {
  if (owner === current) owner = undefined
}
