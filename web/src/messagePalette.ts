type RGB = [number, number, number]

const surface: RGB = [21, 21, 21]
const text: RGB = [239, 238, 235]
const muted: RGB = [181, 178, 173]
const link: RGB = [195, 217, 250]
const edge: RGB = [69, 68, 63]
const maxSurfaceLuminance = luminance(mix(surface, [255, 255, 255], 0.14))

function readColor(value: string): RGB | null {
 const match = /^rgba?\((\d+(?:\.\d+)?), (\d+(?:\.\d+)?), (\d+(?:\.\d+)?)(?:, (\d+(?:\.\d+)?))?\)$/.exec(value)
 if (!match) return null
 const alpha = Number(match[4] ?? 1)
 return alpha ? mix(surface, [Number(match[1]), Number(match[2]), Number(match[3])], alpha) : null
}

function mix(base: RGB, color: RGB, amount: number): RGB {
 return [
  Math.round(base[0] + (color[0] - base[0]) * amount),
  Math.round(base[1] + (color[1] - base[1]) * amount),
  Math.round(base[2] + (color[2] - base[2]) * amount),
 ]
}

function luminance(color: RGB): number {
 const [r, g, b] = color.map(channel => {
  const srgb = channel / 255
  return srgb <= 0.04045 ? srgb / 12.92 : ((srgb + 0.055) / 1.055) ** 2.4
 })
 return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

function hasHue(color: RGB): boolean {
 return Math.max(...color) - Math.min(...color) >= 24
}

function rgb(color: RGB): string {
 return `rgb(${color.join(', ')})`
}

export function applyMessagePalette(body: HTMLElement) {
 const view = body.ownerDocument.defaultView
 if (!view) return

 // Read sender styles as a batch before changing the document. This includes
 // class-based designs and inherited text, not just inline email templates.
 const palette = Array.from(body.querySelectorAll<HTMLElement>('*:not(style)'), element => {
  const style = view.getComputedStyle(element)
  const action = element.closest<HTMLAnchorElement>('a[href]')
  const background = readColor(style.backgroundColor)
  const foreground = readColor(style.color)
  const border = readColor(style.borderTopColor)
  const isButton = element === action && !!background &&
   (style.display === 'inline-block' || parseFloat(style.borderRadius) > 0)
  return { element, action, background, foreground, border, isButton }
 })

 const buttons = new Map<HTMLElement, string>()
 for (const { element, background, isButton } of palette) {
  if (!isButton || !background) continue
  buttons.set(element, luminance(background) > 0.179 ? '#000' : '#fff')
  element.dataset.readerAction = ''
 }

 for (const { element, action, background, foreground, border, isButton } of palette) {
  const buttonText = action && buttons.get(action)
  const tone = action ? link : muted
  const shade = foreground ? luminance(foreground) : 0
  let color = action ? link : shade > 0.1 && shade < 0.45 ? muted : text
  if (foreground && hasHue(foreground)) {
   const tinted = mix(tone, foreground, 0.25)
   color = luminance(tinted) + 0.05 >= 4.5 * (maxSurfaceLuminance + 0.05) ? tinted : mix(tone, foreground, 0.12)
  }

  element.style.setProperty('background', background ? rgb(isButton ? background : mix(surface, background, 0.14)) : 'transparent', 'important')
  element.style.setProperty('color', buttonText || rgb(color), 'important')
  element.style.setProperty('border-color', rgb(border && hasHue(border) ? mix(edge, border, 0.45) : edge), 'important')
 }
}
