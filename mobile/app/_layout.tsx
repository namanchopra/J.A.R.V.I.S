// v0.3.0: rebuilt orb-first. Real entry point lands in TASK-019.
import { Stack } from 'expo-router';
import { StatusBar } from 'expo-status-bar';

export { ErrorBoundary } from 'expo-router';

export default function RootLayout() {
  return (
    <>
      <StatusBar style="light" />
      <Stack screenOptions={{ headerShown: false, contentStyle: { backgroundColor: '#0a0e1a' } }} />
    </>
  );
}
