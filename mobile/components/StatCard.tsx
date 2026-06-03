import React from 'react';
import { Pressable, StyleSheet, Text, View } from 'react-native';
import { colors as hudColors, fontFamilies } from '../lib/hud-tokens';

/**
 * Compact stat tile rendered around the orb on the Friday home screen.
 *
 * Layout per tile (60-70 dp tall, fills its grid cell):
 *   ┌────────────────┐
 *   │ value (big)    │
 *   │ label (small)  │
 *   └────────────────┘
 *
 * The card auto-highlights when ``accent`` is "warn" (pending approvals) or
 * "alert" (failures). Idle tiles use the neutral panel colour from
 * ``hud-tokens`` so they read as dim until something demands attention.
 */
export interface StatCardProps {
  /** Big number / short text rendered prominently. */
  value: string | number;
  /** Lowercase descriptor under the value (e.g. "active", "pending"). */
  label: string;
  /** Visual emphasis. ``default`` = neutral, ``warn`` = amber, ``alert`` = red. */
  accent?: 'default' | 'warn' | 'alert';
  /** Optional tap handler. When set, the card becomes a Pressable. */
  onPress?: () => void;
  /** Optional second-line caption rendered under ``label`` in muted text. */
  caption?: string;
  /** Accessibility label override. Defaults to ``${value} ${label}``. */
  a11yLabel?: string;
  testID?: string;
}

export function StatCard({
  value,
  label,
  accent = 'default',
  onPress,
  caption,
  a11yLabel,
  testID,
}: StatCardProps): React.ReactElement {
  const accentStyle =
    accent === 'warn'
      ? styles.cardWarn
      : accent === 'alert'
      ? styles.cardAlert
      : styles.cardDefault;
  const valueColor =
    accent === 'warn'
      ? hudColors.amber
      : accent === 'alert'
      ? hudColors.red
      : hudColors.textPrimary;

  const content = (
    <>
      <Text style={[styles.value, { color: valueColor }]} numberOfLines={1}>
        {value}
      </Text>
      <Text style={styles.label} numberOfLines={1}>
        {label}
      </Text>
      {caption ? (
        <Text style={styles.caption} numberOfLines={1}>
          {caption}
        </Text>
      ) : null}
    </>
  );

  const accessibility = {
    accessible: true,
    accessibilityLabel: a11yLabel ?? `${value} ${label}`,
    accessibilityRole: (onPress ? 'button' : 'text') as 'button' | 'text',
  };

  if (onPress) {
    return (
      <Pressable
        style={[styles.card, accentStyle]}
        onPress={onPress}
        testID={testID}
        {...accessibility}
      >
        {content}
      </Pressable>
    );
  }
  return (
    <View style={[styles.card, accentStyle]} testID={testID} {...accessibility}>
      {content}
    </View>
  );
}

const styles = StyleSheet.create({
  card: {
    flex: 1,
    minHeight: 66,
    paddingHorizontal: 12,
    paddingVertical: 8,
    borderRadius: 12,
    borderWidth: 1,
    backgroundColor: hudColors.bgPanel,
    justifyContent: 'center',
  },
  cardDefault: {
    borderColor: hudColors.border,
  },
  cardWarn: {
    borderColor: hudColors.amber,
    backgroundColor: 'rgba(255, 170, 0, 0.08)',
  },
  cardAlert: {
    borderColor: hudColors.red,
    backgroundColor: 'rgba(255, 68, 68, 0.10)',
  },
  value: {
    fontSize: 22,
    fontWeight: '600',
    letterSpacing: 0.5,
    fontFamily: fontFamilies.mono,
  },
  label: {
    fontSize: 11,
    color: hudColors.textDim,
    textTransform: 'uppercase',
    letterSpacing: 0.8,
    marginTop: 2,
    fontFamily: fontFamilies.mono,
  },
  caption: {
    fontSize: 10,
    color: hudColors.textDim,
    marginTop: 2,
    opacity: 0.8,
    fontFamily: fontFamilies.mono,
  },
});
