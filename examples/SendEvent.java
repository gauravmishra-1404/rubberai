// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Gaurav Mishra
import java.net.URI;
import java.net.http.*;
import java.time.*;
import java.util.UUID;

// Run with Java 17+: java examples/SendEvent.java
class SendEvent {
    public static void main(String[] args) throws Exception {
        String project = System.getenv("RUBBERAI_PROJECT_ID");
        if (project == null || !project.matches("[a-zA-Z0-9_.:@/-]{1,160}"))
            throw new IllegalArgumentException("Set a valid RUBBERAI_PROJECT_ID");
        String body = """
            {"event_id":"%s","event_type":"tool.completed","project_id":"%s",
             "timestamp":"%s","user_id":"java-developer","trace_id":"example-trace",
             "tool_name":"test","status":"passed","language":"java"}
            """.formatted(UUID.randomUUID(), project, Instant.now());
        String base = System.getenv().getOrDefault("RUBBERAI_URL", "http://localhost:8080");
        var request = HttpRequest.newBuilder(URI.create(base + "/api/v1/events"))
            .timeout(Duration.ofSeconds(10)).header("Content-Type", "application/json")
            .header("Authorization", "Bearer " + System.getenv("RUBBERAI_API_KEY"))
            .POST(HttpRequest.BodyPublishers.ofString(body)).build();
        var response = HttpClient.newHttpClient().send(request, HttpResponse.BodyHandlers.ofString());
        System.out.println(response.statusCode() + " " + response.body());
    }
}
