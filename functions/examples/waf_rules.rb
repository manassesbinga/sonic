# functions/examples/waf_rules.rb — Sonic Native Edge Worker (Ruby)
#
# Runs as a persistent background process.
# Protocol: Reads JSON packet from stdin, processes it, and writes JSON result to stdout.

require 'json'

def log(msg)
  # Print to stdout without { } so the Go runtime captures it as a worker log
  puts "[Ruby Security] #{msg}"
  STDOUT.flush
end

log "Ruby WAF Security Worker initialized successfully."

STDIN.each_line do |line|
  trimmed = line.strip
  next if trimmed.empty?
  
  begin
    packet = JSON.parse(trimmed)
    
    if (packet["protocol"] == "http" || packet["protocol"] == "https") && packet["request"]
      req = packet["request"]
      body = req["body"] || ""
      
      # Simple SQL injection & XSS signature scan
      if body.include?("UNION SELECT") || body.include?("passwd") || body.include?("<script>")
        log "BLOCKED exploit payload signature: #{body[0..80]}..."
        
        # Block the request by returning a direct response
        result = {
          "allow" => false,
          "response" => {
            "status" => 403,
            "headers" => {
              "Content-Type" => "text/plain",
              "X-Security-Block" => "Ruby WAF Engine"
            },
            "body" => "Access Denied: Request blocked by Ruby Security Policy."
          }
        }
        puts JSON.generate(result)
        STDOUT.flush
        next
      end
    end
    
    # Allow other traffic to pass through
    result = {
      "allow" => true,
      "packet" => packet
    }
    puts JSON.generate(result)
    STDOUT.flush
  rescue => e
    log "Error in Ruby processing: #{e.message}"
    puts JSON.generate({"allow" => true})
    STDOUT.flush
  end
end
