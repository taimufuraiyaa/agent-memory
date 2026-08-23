import { Button, createTheme, Modal, Select } from '@mantine/core'

export const agentMemoryTheme = createTheme({
  primaryColor: 'memory',
  primaryShade: 6,
  defaultRadius: 'md',
  respectReducedMotion: true,
  fontFamily: 'Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
  fontFamilyMonospace: '"JetBrains Mono", "SFMono-Regular", Consolas, monospace',
  headings: {
    fontFamily: 'Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
    fontWeight: '650',
  },
  colors: {
    memory: [
      '#effbf3',
      '#dcf4e4',
      '#bce8ca',
      '#98dbad',
      '#7bcf96',
      '#68c887',
      '#5dc47f',
      '#4ba86b',
      '#3d965e',
      '#2f8250',
    ],
    dark: [
      '#f4f7f5',
      '#dfe6e1',
      '#bec9c1',
      '#98a79d',
      '#718277',
      '#4f6156',
      '#35463b',
      '#223128',
      '#16221b',
      '#0d1510',
    ],
  },
  components: {
    Button: Button.extend({ defaultProps: { radius: 'md' } }),
    Modal: Modal.extend({ defaultProps: { centered: true, overlayProps: { backgroundOpacity: 0.72, blur: 4 } } }),
    Select: Select.extend({ defaultProps: { checkIconPosition: 'right', allowDeselect: false } }),
  },
})

