document.body.addEventListener('htmx:afterSwap', function (event) {
    var state = event.detail.xhr.responseText;
    var buttons = document.querySelectorAll('.control-button');
    buttons.forEach(function (button) {
        button.classList.remove('depressed');
    });
    var button = document.getElementById(state + '-button');
    if (button) {
        button.classList.add('depressed');
    }
});

var ctx = document.getElementById('static-error-chart').getContext('2d');
var scatterChart = new Chart(ctx, {
    type: 'scatter',
    data: {
        datasets: [{
            label: 'Scatter Dataset',
            data: []
        }]
    },
    options: {
        scales: {
            x: {
                type: 'linear',
                position: 'bottom',
                ticks: {
                    min: 0,
                    max: 2
                }
            }
        }
    }
});

var socket = new WebSocket("ws://localhost:8081/ws");
socket.onmessage = function (event) {
    var data = JSON.parse(event.data);
    scatterChart.data.datasets[0].data.push({
        x: data.mach,
        y: data.dppPs
    });
    scatterChart.update();

    var serverMessages = document.querySelector(".server-messages");
    var log = document.createElement("p");
    log.textContent = event.data;
    serverMessages.appendChild(log);
    serverMessages.scrollTop = serverMessages.scrollHeight;
};