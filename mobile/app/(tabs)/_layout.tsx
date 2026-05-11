export { ErrorBoundary } from "expo-router";
import React from 'react';
import { TouchableOpacity } from 'react-native';
import FontAwesome from '@expo/vector-icons/FontAwesome';
import { Tabs, router } from 'expo-router';

import { JARVIS, jarvisTabBarTheme, jarvisHeaderTheme } from '@/constants/Colors';

function TabBarIcon(props: {
  name: React.ComponentProps<typeof FontAwesome>['name'];
  color: string;
}) {
  return <FontAwesome size={24} style={{ marginBottom: -3 }} {...props} />;
}

export default function TabLayout() {
  return (
    <Tabs
      screenOptions={{
        ...jarvisTabBarTheme,
        ...jarvisHeaderTheme,
        headerShown: true,
        tabBarActiveTintColor: JARVIS.cyan,
        tabBarInactiveTintColor: JARVIS.textMuted,
      }}>
      <Tabs.Screen
        name="index"
        options={{
          title: 'Dashboard',
          ...jarvisHeaderTheme,
          tabBarIcon: ({ color }) => <TabBarIcon name="dashboard" color={color} />,
        }}
      />
      <Tabs.Screen
        name="jarvis"
        options={{
          title: 'Jarvis',
          ...jarvisHeaderTheme,
          tabBarIcon: ({ color }) => <TabBarIcon name="bolt" color={color} />,
        }}
      />
      <Tabs.Screen
        name="voice"
        options={{
          title: 'Voice',
          ...jarvisHeaderTheme,
          tabBarIcon: ({ color }) => <TabBarIcon name="microphone" color={color} />,
        }}
      />
      <Tabs.Screen
        name="sessions"
        options={{
          title: 'Sessions',
          ...jarvisHeaderTheme,
          tabBarIcon: ({ color }) => <TabBarIcon name="terminal" color={color} />,
          headerRight: () => (
            <TouchableOpacity
              onPress={() => router.push('/launch')}
              style={{ marginRight: 16, padding: 4 }}
              accessibilityRole="button"
              accessibilityLabel="Launch new session"
            >
              <FontAwesome name="plus" size={20} color={JARVIS.cyan} />
            </TouchableOpacity>
          ),
        }}
      />
      <Tabs.Screen
        name="workspaces"
        options={{
          title: 'Workspaces',
          ...jarvisHeaderTheme,
          tabBarIcon: ({ color }) => <TabBarIcon name="folder" color={color} />,
        }}
      />
      <Tabs.Screen
        name="activity"
        options={{
          title: 'Activity',
          ...jarvisHeaderTheme,
          tabBarIcon: ({ color }) => <TabBarIcon name="list" color={color} />,
        }}
      />
      <Tabs.Screen
        name="settings"
        options={{
          title: 'Settings',
          ...jarvisHeaderTheme,
          tabBarIcon: ({ color }) => <TabBarIcon name="cog" color={color} />,
        }}
      />
    </Tabs>
  );
}
