import { StyleSheet } from 'react-native';

// Jarvis sci-fi color palette — matches desktop frontend/src/style.css
export const JARVIS = {
  // Backgrounds
  bg: '#0a0e1a',           // deep space navy (--bg-app)
  surface: '#111827',       // panel surface (--bg-surface)
  elevated: '#1a2332',      // elevated panel (--bg-elevated)

  // Borders
  border: 'rgba(0, 229, 255, 0.15)',
  borderBright: 'rgba(0, 229, 255, 0.3)',
  borderActive: 'rgba(0, 229, 255, 0.6)',

  // Text
  text: '#e8f4ff',
  textSecondary: '#8ba4b8',
  textMuted: '#4a6278',

  // Accent colors
  cyan: '#00e5ff',
  cyanDim: 'rgba(0, 229, 255, 0.1)',
  cyanFaint: 'rgba(0, 229, 255, 0.08)',
  cyanMid: 'rgba(0, 229, 255, 0.3)',
  green: '#00ff88',
  greenDim: 'rgba(0, 255, 136, 0.1)',
  red: '#ff4757',
  redDim: 'rgba(255, 71, 87, 0.1)',
  amber: '#ffb800',
  amberDim: 'rgba(255, 184, 0, 0.1)',
  indigo: '#7c5cbf',

  // Chat / Jarvis screen
  userBubble: '#0d3d5c',
  chatTextMuted: '#7b8fa3',
  inputBg: '#0f1729',
  inputBorder: '#1e293b',
  headerBorder: '#1a2236',
  recording: '#ff4d6a',
  recordingBg: 'rgba(255, 77, 106, 0.12)',

  // Launch screen (light palette)
  lightBg: '#f8f9fa',
  lightSurface: '#ffffff',
  lightSurfaceAlt: '#e9ecef',
  lightBorder: '#eeeeee',
  lightBorderInput: '#dddddd',
  lightText: '#333333',
  lightTextSecondary: '#555555',
  lightTextMuted: '#888888',
  lightTextDimmer: '#999999',
  lightTextFaint: '#666666',
  lightTextPlaceholder: '#777777',
  lightError: '#dc3545',
  lightAccent: '#2f95dc',
  lightGreen: '#34c759',
  lightSelectedBg: '#f0fdff',
  lightSelectedBorder: '#1a1a2e',
};

// Shared reusable styles
export const jarvisStyles = StyleSheet.create({
  // Screen container
  screen: {
    flex: 1,
    backgroundColor: JARVIS.bg,
  },

  // Holo-panel card (matches desktop .holo-panel)
  holoPanel: {
    backgroundColor: JARVIS.surface,
    borderWidth: 1,
    borderColor: JARVIS.border,
    borderRadius: 8,
    padding: 12,
  },

  // Glow border card
  glowCard: {
    backgroundColor: JARVIS.surface,
    borderWidth: 1,
    borderColor: JARVIS.borderBright,
    borderRadius: 8,
    padding: 12,
    // Note: RN doesn't support box-shadow glow like CSS
    // Use elevation on Android for subtle lift
    elevation: 2,
  },

  // Section header text
  sectionHeader: {
    fontSize: 11,
    fontWeight: '700',
    letterSpacing: 1.5,
    textTransform: 'uppercase',
    color: JARVIS.cyan,
    fontFamily: 'SpaceMono',
  },

  // Dark input field
  input: {
    backgroundColor: JARVIS.elevated,
    borderWidth: 1,
    borderColor: JARVIS.border,
    borderRadius: 6,
    paddingHorizontal: 12,
    paddingVertical: 8,
    color: JARVIS.text,
    fontSize: 14,
  },

  // Header bar style
  header: {
    backgroundColor: JARVIS.surface,
    borderBottomWidth: 1,
    borderBottomColor: JARVIS.border,
  },

  // Monospace text for data/terminal
  mono: {
    fontFamily: 'SpaceMono',
    color: JARVIS.text,
  },

  // Status indicator dot
  statusDot: {
    width: 8,
    height: 8,
    borderRadius: 4,
  },
});

// Tab bar theme options (for _layout.tsx)
export const jarvisTabBarTheme = {
  tabBarStyle: {
    backgroundColor: JARVIS.bg,
    borderTopWidth: 1,
    borderTopColor: JARVIS.border,
  },
  tabBarActiveTintColor: JARVIS.cyan,
  tabBarInactiveTintColor: JARVIS.textMuted,
};

// Stack header theme (for _layout.tsx)
export const jarvisHeaderTheme = {
  headerStyle: { backgroundColor: JARVIS.surface },
  headerTintColor: JARVIS.cyan,
  headerTitleStyle: {
    color: JARVIS.text,
    fontWeight: '600' as const,
    letterSpacing: 0.5,
  },
  headerShadowVisible: false,
};

// Keep backwards compat for existing code that imports default
export default {
  light: {
    text: JARVIS.text,
    background: JARVIS.bg,
    tint: JARVIS.cyan,
    tabIconDefault: JARVIS.textMuted,
    tabIconSelected: JARVIS.cyan,
  },
  dark: {
    text: JARVIS.text,
    background: JARVIS.bg,
    tint: JARVIS.cyan,
    tabIconDefault: JARVIS.textMuted,
    tabIconSelected: JARVIS.cyan,
  },
};
