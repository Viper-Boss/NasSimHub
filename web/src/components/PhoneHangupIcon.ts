import { h, type FunctionalComponent } from 'vue'

/**
 * PhoneHangup — a handset rotated so it reads unambiguously as "hang up".
 *
 * Why this exists instead of `PhoneOff` from `@lucide/vue`:
 *
 * `PhoneOff` draws a handset plus a full-cell diagonal slash (`M22 2 2 22`).
 * `MicOff` draws a microphone plus a nearly identical diagonal slash
 * (`m2 2 20 20`). At the 25 px size used on the call buttons the two glyphs are
 * visually indistinguishable, so the hangup button was being read as
 * "microphone muted" (reported as "挂断按钮不对"). Lucide ships no dedicated
 * hangup icon, so this component supplies one.
 *
 * Geometry: Lucide's `Phone` handset path (unchanged) inside a group that is
 * rotated 135° and scaled to 0.9 about the 24×24 viewBox centre. The scale is
 * required because the handset's furthest point sits ~13.3 units from the
 * centre; without it the rotation would push the handset ends past the viewBox
 * edge and the outer `<svg>` would clip them.
 *
 * The transform list is written centre-first (`translate(12 12) … translate(-12 -12)`)
 * so that both the scale and the rotation pivot on the viewBox centre.
 */

/** Lucide `Phone` handset outline (v1.28.0), 24×24 coordinate space. */
const HANDSET_PATH =
  'M13.832 16.568a1 1 0 0 0 1.213-.303l.355-.465A2 2 0 0 1 17 15h3a2 2 0 0 1 2 2v3a2 2 0 0 1-2 2A18 18 0 0 1 2 4a2 2 0 0 1 2-2h3a2 2 0 0 1 2 2v3a2 2 0 0 1-.8 1.6l-.468.351a1 1 0 0 0-.292 1.233 14 14 0 0 0 6.392 6.384'

const HANDSET_TRANSFORM = 'translate(12 12) rotate(135) scale(0.9) translate(-12 -12)'

export interface PhoneHangupProps {
  /** Rendered width/height in px. Mirrors the `:size` prop of `@lucide/vue` icons. */
  size?: number | string
  /** Stroke width in viewBox units. Defaults to 2, matching Lucide. */
  strokeWidth?: number | string
}

/**
 * Hangup glyph for call buttons. Accepts the same `:size` prop as every
 * `@lucide/vue` icon, so it drops into the existing markup unchanged. Any
 * extra attributes (`class`, `aria-*`, …) fall through to the `<svg>`.
 */
export const PhoneHangup: FunctionalComponent<PhoneHangupProps> = (props, { attrs }) => {
  const size = props.size ?? 24
  return h(
    'svg',
    {
      xmlns: 'http://www.w3.org/2000/svg',
      width: size,
      height: size,
      viewBox: '0 0 24 24',
      fill: 'none',
      stroke: 'currentColor',
      'stroke-width': props.strokeWidth ?? 2,
      'stroke-linecap': 'round',
      'stroke-linejoin': 'round',
      class: 'lucide lucide-phone-hangup',
      'aria-hidden': 'true',
      ...attrs
    },
    [h('g', { transform: HANDSET_TRANSFORM }, [h('path', { d: HANDSET_PATH })])]
  )
}

export default PhoneHangup
