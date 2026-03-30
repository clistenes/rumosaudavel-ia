package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/joho/godotenv"
	"github.com/jung-kurt/gofpdf"
)

// Estruturas de dados
type RespostasInput struct {
	Funcionario string           `json:"funcionario"`
	Dimensoes   map[string][]int `json:"dimensoes"`
}

type Risk struct {
	Dimensao      string `json:"dimensao"`
	Probabilidade int    `json:"probabilidade"`
	Severidade    int    `json:"severidade"`
	NivelRisco    string `json:"nivel_risco"`
	CorPGR        string `json:"cor_pgr"`
	Recomendacao  string `json:"recomendacao_nr1"`
}

type GeminiAIStudio struct {
	ScoreGeral int    `json:"score_geral_saude"`
	MatrizPGR  []Risk `json:"matriz_pgr"`
	Conclusao  string `json:"conclusao_diagnostica"`
}

func main() {
	fmt.Println(">>> Iniciando aplicação...")
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Erro ao carregar o arquivo .env")
	}
	fmt.Println(">>> Arquivo .env carregado com sucesso.")

	app := fiber.New()

	app.Post("/api/v1/pgr-psicossocial", func(c *fiber.Ctx) error {
		fmt.Println("\n--- Nova requisição recebida ---")
		var input RespostasInput
		if err := c.BodyParser(&input); err != nil {
			fmt.Println("ERR: Falha ao parsear JSON de entrada:", err)
			return c.Status(400).JSON(fiber.Map{"error": "JSON inválido"})
		}
		fmt.Printf(">>> Processando funcionário: %s\n", input.Funcionario)

		scoresBrutos := calcularScoresCopsoqBr(input.Dimensoes)
		fmt.Println(">>> Scores calculados internamente.")

		fmt.Println(">>> Consultando Gemini AI Studio...")
		analise, err := consultarGeminiAIStudio(input.Funcionario, scoresBrutos)
		if err != nil {
			fmt.Println("ERR: Falha na comunicação com Gemini:", err)
			return c.Status(500).JSON(fiber.Map{"error": "Erro na IA: " + err.Error()})
		}
		fmt.Println(">>> Resposta da IA recebida e parseada com sucesso.")

		pdfPath := fmt.Sprintf("relatorio_nr1_%s.pdf", strings.ReplaceAll(input.Funcionario, " ", "_"))
		fmt.Printf(">>> Gerando PDF em: %s\n", pdfPath)
		err = gerarPDFColorido(input.Funcionario, analise, pdfPath)
		if err != nil {
			fmt.Println("ERR: Falha ao gerar PDF:", err)
			return c.Status(500).JSON(fiber.Map{"error": "Erro ao gerar PDF"})
		}

		fmt.Println(">>> Relatório finalizado. Enviando para o cliente.")
		return c.Download(pdfPath)
	})

	fmt.Println(">>> Servidor rodando na porta 3000")
	log.Fatal(app.Listen("0.0.0.0:3000"))
}

func calcularScoresCopsoqBr(dimensoes map[string][]int) map[string]float64 {
	resultados := make(map[string]float64)
	for nome, valores := range dimensoes {
		soma := 0
		for _, v := range valores {
			soma += v
		}
		media := (float64(soma) / float64(len(valores)*5)) * 100
		resultados[nome] = media
	}
	return resultados
}

func consultarGeminiAIStudio(funcionario string, dados map[string]float64) (*GeminiAIStudio, error) {
	apiKey := os.Getenv("GOOGLE_API_KEY")

	// Modelo 2.0 Flash (conforme disponível na sua tela)
	modelName := "gemini-2.0-flash"
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", modelName, apiKey)

	fmt.Printf(">>> Chamando API Gemini 2.0 Flash para: %s\n", funcionario)

	promptText := fmt.Sprintf("Analise os scores COPSOQ II-Br do funcionário %s: %v. Retorne o JSON da matriz de risco conforme as instruções de sistema.", funcionario, dados)

	payload := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]interface{}{
					{"text": promptText},
				},
			},
		},
		"generationConfig": map[string]interface{}{
			"temperature":      0.1,
			"response_mime_type": "application/json",
		},
	}

	jsonData, _ := json.Marshal(payload)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		bodyErr, _ := io.ReadAll(resp.Body)
		fmt.Printf("ERR: Status %d. Detalhes: %s\n", resp.StatusCode, string(bodyErr))
		return nil, fmt.Errorf("Erro na API: %d", resp.StatusCode)
	}

	var geminiRaw struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&geminiRaw); err != nil {
		return nil, err
	}

	if len(geminiRaw.Candidates) == 0 {
		return nil, fmt.Errorf("IA não retornou candidatos")
	}

	rawText := geminiRaw.Candidates[0].Content.Parts[0].Text

	// Limpeza para garantir que o Unmarshal não falhe com caracteres extras
	cleanJSON := strings.TrimSpace(rawText)
	if strings.HasPrefix(cleanJSON, "```") {
		cleanJSON = strings.TrimPrefix(cleanJSON, "```json")
		cleanJSON = strings.TrimPrefix(cleanJSON, "```")
		cleanJSON = strings.TrimSuffix(cleanJSON, "```")
		cleanJSON = strings.TrimSpace(cleanJSON)
	}

	var finalResponse GeminiAIStudio
	err = json.Unmarshal([]byte(cleanJSON), &finalResponse)
	if err != nil {
		fmt.Println("ERR: JSON da IA inválido. Texto bruto:", cleanJSON)
		return nil, err
	}

	return &finalResponse, nil
}

func gerarPDFColorido(nome string, data *GeminiAIStudio, path string) error {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.AddPage()

	cores := map[string][]int{
		"Verde":    {144, 238, 144},
		"Amarelo":  {255, 255, 0},
		"Laranja":  {255, 165, 0},
		"Vermelho": {255, 99, 71},
	}

	pdf.SetFont("Arial", "B", 16)
	pdf.Cell(0, 10, "Inventario de Riscos Psicossociais (NR-01 / COPSOQ II-Br)")
	pdf.Ln(12)

	pdf.SetFont("Arial", "", 11)
	pdf.Cell(0, 10, fmt.Sprintf("Funcionario: %s", nome))
	pdf.Ln(8)
	pdf.Cell(0, 10, fmt.Sprintf("Indice de Saude Mental Global: %d/100", data.ScoreGeral))
	pdf.Ln(15)

	pdf.SetFillColor(200, 200, 200)
	pdf.SetFont("Arial", "B", 9)
	pdf.CellFormat(60, 10, "Dimensao Psicossocial", "1", 0, "C", true, 0, "")
	pdf.CellFormat(20, 10, "Prob (P)", "1", 0, "C", true, 0, "")
	pdf.CellFormat(20, 10, "Sever (S)", "1", 0, "C", true, 0, "")
	pdf.CellFormat(40, 10, "Nivel de Risco", "1", 0, "C", true, 0, "")
	pdf.CellFormat(50, 10, "Acao Recomendada", "1", 1, "C", true, 0, "")

	pdf.SetFont("Arial", "", 8)
	for _, r := range data.MatrizPGR {
		c, ok := cores[r.CorPGR]
		if !ok { c = []int{255, 255, 255} }

		pdf.CellFormat(60, 10, r.Dimensao, "1", 0, "L", false, 0, "")
		pdf.CellFormat(20, 10, fmt.Sprintf("%d", r.Probabilidade), "1", 0, "C", false, 0, "")
		pdf.CellFormat(20, 10, fmt.Sprintf("%d", r.Severidade), "1", 0, "C", false, 0, "")

		pdf.SetFillColor(c[0], c[1], c[2])
		pdf.CellFormat(40, 10, r.NivelRisco, "1", 0, "C", true, 0, "")
		pdf.CellFormat(50, 10, r.Recomendacao, "1", 1, "L", false, 0, "")
	}

	pdf.Ln(10)
	pdf.SetFont("Arial", "B", 11)
	pdf.Cell(0, 10, "Conclusao Diagnostica e Parecer Tecnico:")
	pdf.Ln(8)
	pdf.SetFont("Arial", "", 10)
	pdf.MultiCell(0, 5, data.Conclusao, "", "L", false)

	return pdf.OutputFileAndClose(path)
}